package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thescaffold/gox-packages-core/security"
	"github.com/thescaffold/gox-packages-core/utils"
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
func (c *Client) Internal(method, rawURL string, body, queries, headers any, retryCount int) (bool, int, string, any) {
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

	return c.request(method, rawURL, body, toStringMap(queries), h, retryCount)
}

// External makes an unauthenticated outbound HTTP call.
func (c *Client) External(method, rawURL string, body, queries, headers any, retryCount int) (bool, int, string, any) {
	h := toStringMap(headers)
	if _, ok := h["content-type"]; !ok {
		h["content-type"] = "application/json"
	}
	return c.request(method, rawURL, body, toStringMap(queries), h, retryCount)
}

// request is the shared retry loop. On non-2xx it retries retryCount more times
// with 100 ms delay, matching TS retry({ delay: 100, count: retryCount }).
func (c *Client) request(method, rawURL string, body any, queries, headers map[string]string, retryCount int) (bool, int, string, any) {
	method = strings.ToUpper(method)

	// Append query parameters
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
		respData   any
	)

	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		var bodyReader io.Reader
		if body != nil && method != http.MethodGet {
			data, err := json.Marshal(body)
			if err == nil {
				bodyReader = bytes.NewReader(data)
			}
		}

		success, statusCode, statusText, respData = c.do(method, rawURL, bodyReader, headers)
		if success {
			return success, statusCode, statusText, respData
		}
	}
	return success, statusCode, statusText, respData
}

func (c *Client) do(method, rawURL string, body io.Reader, headers map[string]string) (bool, int, string, any) {
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return false, 503, "Service is Down", err.Error()
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, 503, "Service is Down",
			"One of our service is temporary down. We are on it, it would be back soon."
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		data = string(raw)
	}

	ok := resp.StatusCode >= 200 && resp.StatusCode <= 299
	return ok, resp.StatusCode, resp.Status, data
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
