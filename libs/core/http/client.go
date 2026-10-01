package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thescaffold/gox-packages/libs/core/security"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// Client is a retry-capable HTTP client for inter-service calls.
// HMACKey is used by Internal to sign x-ntx-nonce/x-ntx-timestamp headers.
type Client struct {
	HMACKey    string
	HTTPClient *http.Client
}

// New returns a Client with a default 30-second timeout.
func New(hmacKey string) *Client {
	return &Client{
		HMACKey:    hmacKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Internal makes an authenticated internal service call.
// Adds HMAC headers (x-ntx-nonce, x-ntx-timestamp, authorization: hmac <sig>)
// mirroring TS QuickHttpService.internal() exactly.
//
// Returns the same 5-tuple as TS: (success, status, statusText, errBody, data).
// errBody is non-nil only when the underlying request errored before a response
// was received (slot reserved for parity with TS [success, status, statusText, error, data]).
func (c *Client) Internal(method, rawURL string, body, queries, headers any, retryCount int) (bool, int, string, any, any) {
	nonce := utils.UUID()
	timestamp := fmt.Sprintf("%d", time.Now().UTC().UnixMilli())
	sig, _ := security.GenerateHmac(map[string]any{"nonce": nonce, "timestamp": timestamp}, "sha256", c.HMACKey)

	h := toStringMap(headers)
	h["x-ntx-nonce"] = nonce
	h["x-ntx-timestamp"] = timestamp
	h["authorization"] = "hmac " + sig
	if _, ok := h["content-type"]; !ok {
		h["content-type"] = "application/json"
	}

	return c.request(method, rawURL, body, toStringMap(queries), h, retryCount, false)
}

// External makes an unauthenticated outbound HTTP call.
// Returns the same 5-tuple shape as Internal — see Internal docstring.
func (c *Client) External(method, rawURL string, body, queries, headers any, retryCount int) (bool, int, string, any, any) {
	h := toStringMap(headers)
	if _, ok := h["content-type"]; !ok {
		h["content-type"] = "application/json"
	}
	return c.request(method, rawURL, body, toStringMap(queries), h, retryCount, false)
}

// Request is the public no-auth entry point used by jsx-style HTTP wrappers
// (blobs/flags/polylog) that supply their own bearer-token auth header.
// Matches TS jsx-* request() shape: returns (success, status, statusText, errBody, data).
// jsx request() uses axios `validateStatus: status < 500`, so any response
// below 500 (including 4xx) is treated as success and its body returned.
func (c *Client) Request(method, rawURL string, body, queries, headers any, retryCount int) (bool, int, string, any, any) {
	h := toStringMap(headers)
	if _, ok := h["content-type"]; !ok {
		h["content-type"] = "application/json"
	}
	return c.request(method, rawURL, body, toStringMap(queries), h, retryCount, true)
}

// request is the shared retry loop. On a non-success response it retries
// retryCount more times with 100 ms delay, matching TS retry({ delay: 100,
// count: retryCount }). When jsxMode is true, success is "status < 500"
// (mirroring jsx-* axios validateStatus); otherwise it is "2xx".
func (c *Client) request(method, rawURL string, body any, queries, headers map[string]string, retryCount int, jsxMode bool) (bool, int, string, any, any) {
	method = strings.ToUpper(method)

	if len(queries) > 0 {
		u, err := url.Parse(rawURL)
		if err == nil {
			vals := u.Query()
			for k, v := range queries {
				vals.Set(k, v)
			}
			u.RawQuery = vals.Encode()
			rawURL = u.String()
		}
	}

	attempts := retryCount + 1
	var (
		success    bool
		statusCode int
		statusText string
		errBody    any
		respData   any
	)

	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		var bodyReader io.Reader
		reqHeaders := headers
		if body != nil && method != http.MethodGet {
			data, contentType, err := encodeBody(body, headers["content-type"])
			if err == nil {
				bodyReader = bytes.NewReader(data)
				if contentType != headers["content-type"] {
					reqHeaders = copyHeaders(headers)
					reqHeaders["content-type"] = contentType
				}
			}
		}

		success, statusCode, statusText, errBody, respData = c.do(method, rawURL, bodyReader, reqHeaders, jsxMode)
		if success {
			return success, statusCode, statusText, errBody, respData
		}
	}
	return success, statusCode, statusText, errBody, respData
}

func (c *Client) do(method, rawURL string, body io.Reader, headers map[string]string, jsxMode bool) (bool, int, string, any, any) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		// TS catch (no response): [false, 503, 'Service is Down', <friendly msg>, <exception>].
		return false, 503, "Service is Down",
			"One of our service is temporary down. We are on it, it would be back soon.",
			err.Error()
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// TS catch (no response): [false, 503, 'Service is Down', <friendly msg>, <exception>].
		// The friendly message goes in the errBody slot (3); the raw error goes
		// in the data slot (4) — mirroring TS exactly.
		return false, 503, "Service is Down",
			"One of our service is temporary down. We are on it, it would be back soon.",
			err.Error()
	}
	defer resp.Body.Close()

	// Cap response body size — without this, a misbehaving / hostile upstream
	// could stream gigabytes into memory and OOM the process. 50 MiB is
	// generous for normal JSON API responses while still bounded.
	const maxResponseBody = 50 * 1024 * 1024
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		data = string(raw)
	}

	ok := resp.StatusCode >= 200 && resp.StatusCode <= 299
	if jsxMode {
		// jsx-* axios validateStatus: status < 500 is a non-throwing response.
		ok = resp.StatusCode < 500
	}
	// axios exposes the reason phrase only (e.g. "OK"), not Go's "200 OK" status
	// line; strip the leading numeric code to match response.statusText.
	statusText := resp.Status
	if i := strings.IndexByte(resp.Status, ' '); i >= 0 {
		statusText = resp.Status[i+1:]
	}
	return ok, resp.StatusCode, statusText, nil, data
}

// toStringMap coerces headers/queries to map[string]string.
func toStringMap(v any) map[string]string {
	out := map[string]string{}
	if v == nil {
		return out
	}
	switch m := v.(type) {
	case map[string]string:
		for k, val := range m {
			out[k] = val
		}
	case map[string]any:
		for k, val := range m {
			out[k] = fmt.Sprintf("%v", val)
		}
	}
	return out
}

func copyHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

// encodeBody serialises body for the declared content type: a form type sends a
// real form (multipart with its boundary, or url-encoded), anything else JSON.
// It returns the content type to send, which carries the boundary for multipart.
func encodeBody(body any, contentType string) ([]byte, string, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch mediaType {
	case "application/x-www-form-urlencoded":
		fields, err := formFields(body)
		if err != nil {
			return nil, contentType, err
		}
		v := url.Values{}
		for k, vals := range fields {
			for _, x := range vals {
				v.Add(k, x)
			}
		}
		return []byte(v.Encode()), contentType, nil
	case "multipart/form-data":
		fields, err := formFields(body)
		if err != nil {
			return nil, contentType, err
		}
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, vals := range fields {
			for _, x := range vals {
				if err := w.WriteField(k, x); err != nil {
					return nil, contentType, err
				}
			}
		}
		if err := w.Close(); err != nil {
			return nil, contentType, err
		}
		return buf.Bytes(), w.FormDataContentType(), nil
	}
	data, err := json.Marshal(body)
	return data, contentType, err
}

// formFields flattens a JSON-shaped body into form fields: a scalar becomes one
// value, a list becomes repeated values, and a nested value is sent as JSON.
func formFields(body any) (map[string][]string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("http: a form body must be an object: %w", err)
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case nil:
			continue
		case []any:
			for _, e := range t {
				out[k] = append(out[k], formScalar(e))
			}
		default:
			out[k] = []string{formScalar(t)}
		}
	}
	return out, nil
}

func formScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
