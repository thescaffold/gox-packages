// Package platform ports ntx-packages/libs/core/src/services/platform.service.ts.
// PlatformService is a thin façade over the core HTTP client that calls
// the standard scaffold app endpoints (controller/identity/capital/common/etc.)
// using HMAC-signed internal authentication.
//
// Context propagation: callers that need to forward request-scoped headers
// (x-ntx-user-id, x-ntx-tracing-id, x-ntx-preference, …) onto outbound
// platform calls should use WithContext(NTXContext) to obtain a context-bound
// copy of the service. The copy preserves the HMAC signing behaviour and
// merges per-call headers on top of the context-derived ones.
package platform

import (
	"fmt"
	"strings"

	ntxctx "github.com/thescaffold/gox-packages/libs/core/context"
	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// Service is the platform façade. Construct with New(client, baseURL) or
// NewWithBaseURLFn(client, fn) for dynamic BASE_URL resolution (TS reads the
// env via configService.get on every call — gox can mirror that with a
// closure).
type Service struct {
	client         *corehttp.Client
	baseURLFn      func() string
	defaultHeaders map[string]string
}

// New constructs a PlatformService rooted at baseURL (typically the scaffold
// gateway URL). The HMAC client is what authenticates inter-service calls.
func New(client *corehttp.Client, baseURL string) *Service {
	base := strings.TrimRight(baseURL, "/")
	return &Service{
		client:    client,
		baseURLFn: func() string { return base },
	}
}

// NewWithBaseURLFn builds a PlatformService whose BASE_URL is resolved on
// each call via fn(). Use this when BASE_URL may change at runtime (env
// reload, multi-tenant deployments, etc.). Trailing slashes returned by fn
// are stripped at call time.
func NewWithBaseURLFn(client *corehttp.Client, fn func() string) *Service {
	if fn == nil {
		fn = func() string { return "" }
	}
	return &Service{client: client, baseURLFn: fn}
}

// BaseURL returns the configured base URL with any trailing slash stripped.
func (s *Service) BaseURL() string {
	if s == nil || s.baseURLFn == nil {
		return ""
	}
	return strings.TrimRight(s.baseURLFn(), "/")
}

// WithContext returns a copy of the service whose outbound calls carry the
// standard x-ntx-* context headers derived from ctx. Subsequent per-call
// headers (passed to Request) override the context-derived ones key-wise.
//
// Mirrors TS PlatformService injecting QuickHttpService which reads request-
// scoped context and prepends headers via formatHeaders(). The header set is
// produced by ntxctx.Format() and matches utils/common.util.ts formatHeaders().
// A fresh x-ntx-tracing-id is minted when the inbound context lacks one, so
// every outbound call is traceable.
func (s *Service) WithContext(c ntxctx.NTXContext) *Service {
	if c.TracingID == "" {
		c.TracingID = utils.Reference("TID", 36)
	}
	cp := *s
	cp.defaultHeaders = ntxctx.Format(c)
	return &cp
}

// Result mirrors TS [status, message, data] return triple from each method.
type Result struct {
	Status  bool
	Message string
	Data    any
}

// Request makes an arbitrary HMAC-signed call. Mirrors TS PlatformService.request.
func (s *Service) Request(method, url string, body, queries, headers any, retryCount int) Result {
	ok, _, _, _, resp := s.client.Internal(method, url, body, queries, s.mergeHeaders(headers), retryCount)
	return s.unwrap(ok, resp)
}

// AppsBaseURL returns "{baseURL}/apps".
func (s *Service) AppsBaseURL() string {
	return s.BaseURL() + "/apps"
}

// ── apps.controller.get.route ────────────────────────────────────────────────

func (s *Service) ControllerGetRoute(perPage int) Result {
	url := s.AppsBaseURL() + "/controller/route"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"perPage": fmt.Sprintf("%d", perPage)}, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.identity ────────────────────────────────────────────────────────────

func (s *Service) IdentityGetContext(userID string) Result {
	url := fmt.Sprintf("%s/identity/context/%s", s.AppsBaseURL(), userID)
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

func (s *Service) IdentityVerifyToken(authorization string) Result {
	url := s.AppsBaseURL() + "/identity/verify-token"
	ok, _, _, _, resp := s.client.Internal("POST", url, map[string]any{"authorization": authorization}, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.capital ─────────────────────────────────────────────────────────────

func (s *Service) CapitalGetStatus() Result {
	url := s.AppsBaseURL() + "/capital/status"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

func (s *Service) CapitalGetUserPlan(userID, clientID, workspaceID string) Result {
	url := s.AppsBaseURL() + "/capital/user-plan"
	queries := map[string]string{}
	if userID != "" {
		queries["userId"] = userID
	}
	if clientID != "" {
		queries["clientId"] = clientID
	}
	if workspaceID != "" {
		queries["workspaceId"] = workspaceID
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.common ──────────────────────────────────────────────────────────────

func (s *Service) CommonGetCurrency(currency string) Result {
	url := fmt.Sprintf("%s/apps/currency/%s", s.AppsBaseURL(), currency)
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

func (s *Service) CommonGetLocation(ip string) Result {
	url := s.AppsBaseURL() + "/apps/location"
	queries := map[string]string{}
	if ip != "" {
		queries["ip"] = ip
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.assets ──────────────────────────────────────────────────────────────

// AssetsGetDynamic mirrors apps.assets.get.dynamic. Pass payload as a map; the
// canonical shape is {variant: "pixel"|"shapes", store: bool}.
func (s *Service) AssetsGetDynamic(payload map[string]string) Result {
	url := s.AppsBaseURL() + "/assets/dynamic"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, payload, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.figs ────────────────────────────────────────────────────────────────

func (s *Service) FigsPostFile(payload any) Result {
	url := s.AppsBaseURL() + "/figs"
	ok, _, _, _, resp := s.client.Internal("POST", url, payload, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.statics ─────────────────────────────────────────────────────────────

func (s *Service) StaticsGetFilter(key, parentID string) Result {
	url := fmt.Sprintf("%s/statics/filter/%s", s.AppsBaseURL(), key)
	queries := map[string]string{}
	if parentID != "" {
		queries["parentId"] = parentID
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

func (s *Service) StaticsGetCode(code, parentID string) Result {
	url := fmt.Sprintf("%s/statics/code/%s", s.AppsBaseURL(), code)
	var queries map[string]string
	if parentID != "" {
		queries = map[string]string{"parentId": parentID}
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

func (s *Service) StaticsGetValue(value string) Result {
	url := fmt.Sprintf("%s/statics/value/%s", s.AppsBaseURL(), value)
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.cache ───────────────────────────────────────────────────────────────

// CacheGet mirrors apps.cache.get: GET /apps/cache/get?key=...
func (s *Service) CacheGet(key string) Result {
	url := s.AppsBaseURL() + "/cache/get"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"key": key}, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CacheSet mirrors apps.cache.set: POST /apps/cache/set {key,value,duration}.
func (s *Service) CacheSet(key string, value any, duration int) Result {
	url := s.AppsBaseURL() + "/cache/set"
	body := map[string]any{"key": key, "value": value, "duration": duration}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CacheSetnx mirrors apps.cache.setnx: POST /apps/cache/setnx {key,value,duration}.
func (s *Service) CacheSetnx(key string, value any, duration int) Result {
	url := s.AppsBaseURL() + "/cache/setnx"
	body := map[string]any{"key": key, "value": value, "duration": duration}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CacheGetset mirrors apps.cache.getset: POST /apps/cache/getset {key,value,duration}.
func (s *Service) CacheGetset(key string, value any, duration int) Result {
	url := s.AppsBaseURL() + "/cache/getset"
	body := map[string]any{"key": key, "value": value, "duration": duration}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CacheTTL mirrors apps.cache.ttl: POST /apps/cache/ttl {key}.
func (s *Service) CacheTTL(key string) Result {
	url := s.AppsBaseURL() + "/cache/ttl"
	ok, _, _, _, resp := s.client.Internal("POST", url, map[string]any{"key": key}, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CacheIncr mirrors apps.cache.incr: POST /apps/cache/incr {key}.
func (s *Service) CacheIncr(key string) Result {
	url := s.AppsBaseURL() + "/cache/incr"
	ok, _, _, _, resp := s.client.Internal("POST", url, map[string]any{"key": key}, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CacheDel mirrors apps.cache.del: DELETE /apps/cache/del?key=...
func (s *Service) CacheDel(key string) Result {
	url := s.AppsBaseURL() + "/cache/del"
	ok, _, _, _, resp := s.client.Internal("DELETE", url, nil, map[string]string{"key": key}, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.queue ───────────────────────────────────────────────────────────────

// QueuePush mirrors apps.queue.push: POST /apps/queue {queue,job,data,config}.
func (s *Service) QueuePush(queue, job string, data, config any) Result {
	url := s.AppsBaseURL() + "/queue"
	body := map[string]any{"queue": queue, "job": job, "data": data, "config": config}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// QueuePop mirrors apps.queue.pop: GET /apps/queue?queue=...&job=...
func (s *Service) QueuePop(queue, job string) Result {
	url := s.AppsBaseURL() + "/queue"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"queue": queue, "job": job}, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// QueueLog mirrors apps.queue.log: PATCH /apps/queue {jobId,status,output}.
func (s *Service) QueueLog(jobID, logStatus string, output any) Result {
	url := s.AppsBaseURL() + "/queue"
	body := map[string]any{"jobId": jobID, "status": logStatus, "output": output}
	ok, _, _, _, resp := s.client.Internal("PATCH", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.cron ────────────────────────────────────────────────────────────────

// CronRegister mirrors apps.cron.register: POST /apps/cron {group,name,pattern,config}.
func (s *Service) CronRegister(group, name, pattern string, config any) Result {
	url := s.AppsBaseURL() + "/cron"
	body := map[string]any{"group": group, "name": name, "pattern": pattern, "config": config}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CronSelect mirrors apps.cron.select: GET /apps/cron?group=...&name=...
func (s *Service) CronSelect(group, name string) Result {
	url := s.AppsBaseURL() + "/cron"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"group": group, "name": name}, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// CronLog mirrors apps.cron.log: PATCH /apps/cron {jobId,status,output}.
func (s *Service) CronLog(jobID, logStatus string, output any) Result {
	url := s.AppsBaseURL() + "/cron"
	body := map[string]any{"jobId": jobID, "status": logStatus, "output": output}
	ok, _, _, _, resp := s.client.Internal("PATCH", url, body, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// ── apps.origine ─────────────────────────────────────────────────────────────

// OrigineSourceNamespaceCreate mirrors apps.origine.sourceNamespace.create:
// POST /apps/origine/source-namespace with the raw payload.
func (s *Service) OrigineSourceNamespaceCreate(payload any) Result {
	url := s.AppsBaseURL() + "/origine/source-namespace"
	ok, _, _, _, resp := s.client.Internal("POST", url, payload, nil, s.mergeHeaders(nil), 0)
	return s.unwrap(ok, resp)
}

// mergeHeaders combines s.defaultHeaders (context-derived) with the per-call
// headers param. Per-call values win. Returns the merged map[string]string or
// nil when both inputs are empty.
func (s *Service) mergeHeaders(headers any) any {
	if s == nil || len(s.defaultHeaders) == 0 {
		return headers
	}
	out := make(map[string]string, len(s.defaultHeaders)+4)
	for k, v := range s.defaultHeaders {
		out[k] = v
	}
	switch m := headers.(type) {
	case nil:
		// nothing extra
	case map[string]string:
		for k, v := range m {
			out[k] = v
		}
	case map[string]any:
		for k, v := range m {
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

// unwrap converts an HTTP response into the (status, message, data) Result
// shape returned by every TS PlatformService method.
func (s *Service) unwrap(ok bool, resp any) Result {
	r := Result{Status: ok}
	if env, isMap := resp.(map[string]any); isMap {
		if v, ok := env["message"].(string); ok {
			r.Message = v
		}
		r.Data = env["data"]
	}
	return r
}
