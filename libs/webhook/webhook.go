// Package webhook delivers events to customer URLs the way a payments platform
// must: every request is signed (HMAC-SHA256 over timestamp + body, so a
// receiver can reject forgeries and replays), transient failures are retried
// with exponential backoff, permanent ones are not, and each attempt is
// reported so a delivery log can show what happened and a replay can repeat it.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sign returns "t=<unix>,v1=<hex>" where v1 = HMAC-SHA256(secret, "<unix>.<body>").
func Sign(secret string, ts time.Time, body []byte) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	return "t=" + t + ",v1=" + mac(secret, t, body)
}

// SignAll signs with several secrets at once: "t=<unix>,v1=<hex>,v1=<hex>".
// During a secret rotation, send both the new and the previous secret so a
// receiver that has only deployed one of them still verifies (Verify accepts
// any matching v1). A single secret yields exactly what Sign returns.
func SignAll(secrets []string, ts time.Time, body []byte) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	out := "t=" + t
	for _, s := range secrets {
		if s == "" {
			continue
		}
		out += ",v1=" + mac(s, t, body)
	}
	return out
}

func mac(secret, t string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(t))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Errors returned by Verify.
var (
	ErrMalformedHeader = errors.New("webhook: malformed signature header")
	ErrTimestamp       = errors.New("webhook: timestamp outside the tolerance (possible replay)")
	ErrSignature       = errors.New("webhook: signature does not match")
)

// Verify checks a signature header against the raw body. tolerance bounds the
// age of the timestamp (default 5 minutes) to defeat replays. During a secret
// rotation the header may carry several v1= values; any match passes.
func Verify(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	if tolerance == 0 {
		tolerance = 5 * time.Minute
	}
	var t string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return ErrMalformedHeader
		}
		switch k {
		case "t":
			t = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	unix, err := strconv.ParseInt(t, 10, 64)
	if err != nil || len(sigs) == 0 {
		return ErrMalformedHeader
	}
	age := now.Sub(time.Unix(unix, 0))
	if age > tolerance || age < -tolerance {
		return ErrTimestamp
	}
	want := mac(secret, t, body)
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return ErrSignature
}

// Request is one delivery.
type Request struct {
	URL    string
	Method string // default POST
	Secret string // empty sends unsigned
	// Secrets, when set, is used instead of Secret and signs with every entry
	// (secret rotation overlap; see SignAll).
	Secrets   []string
	EventID   string
	EventName string
	Body      []byte
	Headers   map[string]string
}

// Attempt records one try.
type Attempt struct {
	N      int
	Status int    // 0 when no response
	Error  string // network error, if any
	// ResponseBody is the start of the response body (at most
	// MaxResponseBody bytes), kept so a delivery log can show what the
	// receiver said. Empty when there was no response.
	ResponseBody string
	Duration     time.Duration
	At           time.Time
	// Retry says whether the sender will try again after this attempt.
	Retry bool
}

// Result is the outcome of a delivery.
type Result struct {
	Delivered bool
	Attempts  []Attempt
}

// Last returns the final attempt.
func (r Result) Last() Attempt {
	if len(r.Attempts) == 0 {
		return Attempt{}
	}
	return r.Attempts[len(r.Attempts)-1]
}

// MaxResponseBody bounds Attempt.ResponseBody.
const MaxResponseBody = 1024

// Sender performs deliveries.
type Sender struct {
	Client      *http.Client
	Header      string // signature header name (default "X-Signature")
	MaxAttempts int    // default 5
	// Backoff is the wait before attempt n+1 (default 1s doubling, 30s cap).
	Backoff   func(attempt int) time.Duration
	Sleep     func(context.Context, time.Duration)
	Now       func() time.Time
	UserAgent string
	// MaxRetryAfter caps how long a Retry-After header may make us wait (default 2m).
	MaxRetryAfter time.Duration
}

func (s *Sender) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sender) header() string {
	if s.Header != "" {
		return s.Header
	}
	return "X-Signature"
}

func (s *Sender) maxAttempts() int {
	if s.MaxAttempts > 0 {
		return s.MaxAttempts
	}
	return 5
}

func (s *Sender) backoff(n int) time.Duration {
	if s.Backoff != nil {
		return s.Backoff(n)
	}
	d := time.Second << min(n-1, 5)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func (s *Sender) sleep(ctx context.Context, d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(ctx, d)
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// retryable: network errors, 408, 429 and 5xx. Every other status is final.
func retryable(status int, err error) bool {
	if err != nil {
		return true
	}
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// Deliver sends the request, retrying transient failures, and reports every attempt.
func (s *Sender) Deliver(ctx context.Context, r Request) Result {
	var res Result
	method := r.Method
	if method == "" {
		method = http.MethodPost
	}
	for n := 1; n <= s.maxAttempts(); n++ {
		start := s.now()
		status, retryAfter, respBody, err := s.once(ctx, method, r, n)
		a := Attempt{N: n, Status: status, Duration: s.now().Sub(start), At: start, ResponseBody: respBody}
		if err != nil {
			a.Error = err.Error()
		}
		ok := err == nil && status >= 200 && status < 300
		a.Retry = !ok && retryable(status, err) && n < s.maxAttempts() && ctx.Err() == nil
		res.Attempts = append(res.Attempts, a)
		if ok {
			res.Delivered = true
			return res
		}
		if !a.Retry {
			return res
		}
		wait := s.backoff(n)
		if retryAfter > 0 {
			max := s.MaxRetryAfter
			if max == 0 {
				max = 2 * time.Minute
			}
			if retryAfter > max {
				retryAfter = max
			}
			if retryAfter > wait {
				wait = retryAfter
			}
		}
		s.sleep(ctx, wait)
	}
	return res
}

func (s *Sender) once(ctx context.Context, method string, r Request, n int) (status int, retryAfter time.Duration, respBody string, err error) {
	req, err := http.NewRequestWithContext(ctx, method, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return 0, 0, "", err
	}
	ts := s.now()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", firstNonEmpty(s.UserAgent, "gox-webhook/1"))
	req.Header.Set("X-Event-Id", r.EventID)
	req.Header.Set("X-Event-Name", r.EventName)
	req.Header.Set("X-Event-Timestamp", strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set("X-Delivery-Attempt", strconv.Itoa(n))
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	// the signature is made fresh per attempt so the timestamp is never stale
	if len(r.Secrets) > 0 {
		req.Header.Set(s.header(), SignAll(r.Secrets, ts, r.Body))
	} else if r.Secret != "" {
		req.Header.Set(s.header(), Sign(r.Secret, ts, r.Body))
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if len(raw) > MaxResponseBody {
		raw = raw[:MaxResponseBody]
	}
	respBody = string(raw)
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, perr := strconv.Atoi(v); perr == nil && secs > 0 {
			retryAfter = time.Duration(secs) * time.Second
		}
	}
	return resp.StatusCode, retryAfter, respBody, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// String summarises a result for logs.
func (r Result) String() string {
	l := r.Last()
	return fmt.Sprintf("delivered=%v attempts=%d last_status=%d", r.Delivered, len(r.Attempts), l.Status)
}
