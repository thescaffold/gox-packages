// Package wire is what the HTTP drivers without a vendor SDK share (openai,
// gemini, deepseek): one POST with retries before any output, typed provider
// errors, the API key from config or the credential store, and a server-sent
// events reader. Each driver keeps its own request and event translation.
package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// Client posts JSON to one provider.
type Client struct {
	Provider string
	BaseURL  string
	HTTP     *http.Client
	// MaxRetries for a call that fails before any output (429, 5xx, a
	// connection failure). Zero means 2; negative disables retries.
	MaxRetries int
	// Auth sets the credential on a request (the header differs per provider).
	Auth func(req *http.Request, key string)
	// Key resolves the API key for each call, so a rotated key is picked up.
	Key func(ctx context.Context) (string, error)
	// ErrorBody turns an error response body into a message and, when the body
	// says so, a retry delay (Gemini puts it in the body, not a header).
	ErrorBody func(body []byte) (msg string, retryAfter time.Duration)
}

// KeyFrom returns a Key func over the usual pair: a key used as is, else the
// credential store's "apiKey" under ref.
func KeyFrom(provider, apiKey string, store core.CredentialStore, ref string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if apiKey != "" {
			return apiKey, nil
		}
		if store == nil {
			return "", &core.ProviderError{Provider: provider, Kind: core.KindAuth, Message: "no API key or credential store configured"}
		}
		c, err := store.Get(ctx, ref)
		if err != nil {
			return "", &core.ProviderError{Provider: provider, Kind: core.KindAuth, Message: "credential lookup failed", Cause: err}
		}
		if c["apiKey"] == "" {
			return "", &core.ProviderError{Provider: provider, Kind: core.KindAuth, Message: "credential has no apiKey"}
		}
		return c["apiKey"], nil
	}
}

func (c *Client) retries() int {
	switch {
	case c.MaxRetries < 0:
		return 0
	case c.MaxRetries == 0:
		return 2
	}
	return c.MaxRetries
}

// Post sends body to path and returns the open response (2xx only); the caller
// closes it. A non-2xx answer is a *core.ProviderError. Retries happen only
// here, before the caller has read anything.
func (c *Client) Post(ctx context.Context, path string, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	key, err := c.Key(ctx)
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	var last error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		c.Auth(req, key)
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = &core.ProviderError{Provider: c.Provider, Kind: core.KindConnection, Message: err.Error(), Cause: err}
		} else if resp.StatusCode/100 == 2 {
			return resp, nil
		} else {
			last = c.statusError(resp)
		}
		var pe *core.ProviderError
		if !errors.As(last, &pe) || !pe.Retryable() || attempt >= c.retries() {
			return nil, last
		}
		wait := time.Duration(math.Min(float64(500*time.Millisecond)*math.Pow(2, float64(attempt)), float64(8*time.Second)))
		if pe.RetryAfter > 0 && pe.RetryAfter < 30*time.Second {
			wait = pe.RetryAfter
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) statusError(resp *http.Response) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg, after := strings.TrimSpace(string(body)), time.Duration(0)
	if c.ErrorBody != nil {
		if m, a := c.ErrorBody(body); m != "" || a > 0 {
			if m != "" {
				msg = m
			}
			after = a
		}
	}
	if h := RetryAfter(resp.Header); h > 0 {
		after = h
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return &core.ProviderError{Provider: c.Provider, Kind: KindForStatus(resp.StatusCode), Status: resp.StatusCode, RetryAfter: after, Message: msg}
}

// KindForStatus classifies an HTTP status.
func KindForStatus(s int) core.ErrorKind {
	switch {
	case s == 429:
		return core.KindRateLimit
	case s == 404:
		return core.KindNotFound
	case s == 401 || s == 403:
		return core.KindAuth
	case s == 400 || s == 413 || s == 422:
		return core.KindInvalid
	}
	return core.KindStatus
}

// RetryAfter reads Retry-After-Ms or Retry-After (seconds).
func RetryAfter(h http.Header) time.Duration {
	if ms := h.Get("Retry-After-Ms"); ms != "" {
		if n, err := strconv.ParseFloat(ms, 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Millisecond))
		}
	}
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.ParseFloat(s, 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Second))
		}
	}
	return 0
}

// MapError passes context errors and ProviderErrors through and wraps anything
// else as a connection failure.
func MapError(provider string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var pe *core.ProviderError
	if errors.As(err, &pe) {
		return err
	}
	return &core.ProviderError{Provider: provider, Kind: core.KindConnection, Message: err.Error(), Cause: err}
}

// Event is one server-sent event.
type Event struct {
	Name string
	Data []byte
}

// SSE reads server-sent events from a response body.
type SSE struct {
	r   *bufio.Reader
	err error
}

// NewSSE wraps a body.
func NewSSE(body io.Reader) *SSE { return &SSE{r: bufio.NewReaderSize(body, 64<<10)} }

// Next returns the next event with data, or false at the end (see Err).
func (s *SSE) Next() (Event, bool) {
	var ev Event
	var data [][]byte
	for {
		line, err := s.r.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				if len(data) > 0 {
					ev.Data = bytes.Join(data, []byte("\n"))
					return ev, true
				}
				ev = Event{}
			case line[0] == ':':
			default:
				field, value, _ := bytes.Cut(line, []byte(":"))
				value = bytes.TrimPrefix(value, []byte(" "))
				switch string(field) {
				case "event":
					ev.Name = string(value)
				case "data":
					data = append(data, append([]byte(nil), value...))
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				s.err = err
			}
			if len(data) > 0 { // a last event without its blank line
				ev.Data = bytes.Join(data, []byte("\n"))
				return ev, true
			}
			return Event{}, false
		}
	}
}

// Err is the read error that ended the stream, if any (EOF is not one).
func (s *SSE) Err() error { return s.err }

// Out is a stream's output side: every event goes through the shared
// accumulator, so Stream and Chat agree by construction; a send gives up when
// ctx ends; the one terminal event never blocks forever on a consumer that
// stopped reading.
type Out struct {
	Provider string
	Ctx      context.Context
	Ch       chan<- llm.StreamEvent
	Acc      llm.Accumulator
	done     bool
}

// Emit folds e into the accumulator and sends it. An error means stop: the
// caller then calls Fail with it.
func (o *Out) Emit(e llm.StreamEvent) error {
	if err := o.Acc.Add(e); err != nil {
		return err
	}
	select {
	case o.Ch <- e:
		return nil
	case <-o.Ctx.Done():
		return o.Ctx.Err()
	}
}

func (o *Out) terminal(e llm.StreamEvent) {
	if o.done {
		return
	}
	o.done = true
	select {
	case o.Ch <- e:
	case <-time.After(time.Second): // nobody is reading; do not leak
	}
}

// Fail ends the stream with an error (the context's, once it has ended).
func (o *Out) Fail(err error) {
	if o.Ctx.Err() != nil {
		err = o.Ctx.Err()
	}
	o.terminal(llm.StreamEvent{Type: llm.EventError, Err: MapError(o.Provider, err)})
}

// Finish ends the stream with the assembled response.
func (o *Out) Finish(model string) {
	resp, err := o.Acc.Response()
	if err != nil {
		o.Fail(err)
		return
	}
	resp.Model = model
	resp.Provider = o.Provider
	o.terminal(llm.StreamEvent{Type: llm.EventMessageStop, Response: resp})
}

// Done reports whether the terminal event was sent.
func (o *Out) Done() bool { return o.done }

// Incomplete is the error for a stream that closed before it finished.
func Incomplete(provider string) error {
	return &core.ProviderError{Provider: provider, Kind: core.KindConnection, Message: "stream closed before the response was complete"}
}
