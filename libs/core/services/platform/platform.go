// Package platform ports ntx-packages/libs/core/src/services/platform.service.ts.
// PlatformService is a thin façade over the core HTTP client that calls
// the standard scaffold app endpoints (controller/identity/capital/common/etc.)
// using HMAC-signed internal authentication.
package platform

import (
	"fmt"
	"strings"

	corehttp "github.com/thescaffold/gox-packages/libs/core/http"
)

// Service is the platform façade. Construct with New(client, baseURL).
type Service struct {
	client  *corehttp.Client
	baseURL string
}

// New constructs a PlatformService rooted at baseURL (typically the scaffold
// gateway URL). The HMAC client is what authenticates inter-service calls.
func New(client *corehttp.Client, baseURL string) *Service {
	return &Service{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

// Result mirrors TS [status, message, data] return triple from each method.
type Result struct {
	Status  bool
	Message string
	Data    any
}

// Request makes an arbitrary HMAC-signed call. Mirrors TS PlatformService.request.
func (s *Service) Request(method, url string, body, queries, headers any, retryCount int) Result {
	ok, _, _, _, resp := s.client.Internal(method, url, body, queries, headers, retryCount)
	return s.unwrap(ok, resp)
}

// AppsBaseURL returns "{baseURL}/apps".
func (s *Service) AppsBaseURL() string {
	return s.baseURL + "/apps"
}

// ── apps.controller.get.route ────────────────────────────────────────────────

func (s *Service) ControllerGetRoute(perPage int) Result {
	url := s.AppsBaseURL() + "/controller/route"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"perPage": fmt.Sprintf("%d", perPage)}, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.identity ────────────────────────────────────────────────────────────

func (s *Service) IdentityGetContext(userID string) Result {
	url := fmt.Sprintf("%s/identity/context/%s", s.AppsBaseURL(), userID)
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, nil, 0)
	return s.unwrap(ok, resp)
}

func (s *Service) IdentityVerifyToken(authorization string) Result {
	url := s.AppsBaseURL() + "/identity/verify-token"
	ok, _, _, _, resp := s.client.Internal("POST", url, map[string]any{"authorization": authorization}, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.capital ─────────────────────────────────────────────────────────────

func (s *Service) CapitalGetStatus() Result {
	url := s.AppsBaseURL() + "/capital/status"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, nil, 0)
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
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.common ──────────────────────────────────────────────────────────────

func (s *Service) CommonGetCurrency(currency string) Result {
	url := fmt.Sprintf("%s/apps/currency/%s", s.AppsBaseURL(), currency)
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, nil, 0)
	return s.unwrap(ok, resp)
}

func (s *Service) CommonGetLocation(ip string) Result {
	url := s.AppsBaseURL() + "/apps/location"
	queries := map[string]string{}
	if ip != "" {
		queries["ip"] = ip
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.assets ──────────────────────────────────────────────────────────────

// AssetsGetDynamic mirrors apps.assets.get.dynamic. Pass payload as a map; the
// canonical shape is {variant: "pixel"|"shapes", store: bool}.
func (s *Service) AssetsGetDynamic(payload map[string]string) Result {
	url := s.AppsBaseURL() + "/assets/dynamic"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, payload, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.figs ────────────────────────────────────────────────────────────────

func (s *Service) FigsPostFile(payload any) Result {
	url := s.AppsBaseURL() + "/figs"
	ok, _, _, _, resp := s.client.Internal("POST", url, payload, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.statics ─────────────────────────────────────────────────────────────

func (s *Service) StaticsGetFilter(key, parentID string) Result {
	url := fmt.Sprintf("%s/statics/filter/%s", s.AppsBaseURL(), key)
	queries := map[string]string{}
	if parentID != "" {
		queries["parentId"] = parentID
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, nil, 0)
	return s.unwrap(ok, resp)
}

func (s *Service) StaticsGetCode(code, parentID string) Result {
	url := fmt.Sprintf("%s/statics/code/%s", s.AppsBaseURL(), code)
	var queries map[string]string
	if parentID != "" {
		queries = map[string]string{"parentId": parentID}
	}
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, queries, nil, 0)
	return s.unwrap(ok, resp)
}

func (s *Service) StaticsGetValue(value string) Result {
	url := fmt.Sprintf("%s/statics/value/%s", s.AppsBaseURL(), value)
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.cache ───────────────────────────────────────────────────────────────

// CacheGet mirrors apps.cache.get: GET /apps/cache/get?key=...
func (s *Service) CacheGet(key string) Result {
	url := s.AppsBaseURL() + "/cache/get"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"key": key}, nil, 0)
	return s.unwrap(ok, resp)
}

// CacheSet mirrors apps.cache.set: POST /apps/cache/set {key,value,duration}.
func (s *Service) CacheSet(key string, value any, duration int) Result {
	url := s.AppsBaseURL() + "/cache/set"
	body := map[string]any{"key": key, "value": value, "duration": duration}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// CacheSetnx mirrors apps.cache.setnx: POST /apps/cache/setnx {key,value,duration}.
func (s *Service) CacheSetnx(key string, value any, duration int) Result {
	url := s.AppsBaseURL() + "/cache/setnx"
	body := map[string]any{"key": key, "value": value, "duration": duration}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// CacheGetset mirrors apps.cache.getset: POST /apps/cache/getset {key,value,duration}.
func (s *Service) CacheGetset(key string, value any, duration int) Result {
	url := s.AppsBaseURL() + "/cache/getset"
	body := map[string]any{"key": key, "value": value, "duration": duration}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// CacheTTL mirrors apps.cache.ttl: POST /apps/cache/ttl {key}.
func (s *Service) CacheTTL(key string) Result {
	url := s.AppsBaseURL() + "/cache/ttl"
	ok, _, _, _, resp := s.client.Internal("POST", url, map[string]any{"key": key}, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// CacheIncr mirrors apps.cache.incr: POST /apps/cache/incr {key}.
func (s *Service) CacheIncr(key string) Result {
	url := s.AppsBaseURL() + "/cache/incr"
	ok, _, _, _, resp := s.client.Internal("POST", url, map[string]any{"key": key}, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// CacheDel mirrors apps.cache.del: DELETE /apps/cache/del?key=...
func (s *Service) CacheDel(key string) Result {
	url := s.AppsBaseURL() + "/cache/del"
	ok, _, _, _, resp := s.client.Internal("DELETE", url, nil, map[string]string{"key": key}, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.queue ───────────────────────────────────────────────────────────────

// QueuePush mirrors apps.queue.push: POST /apps/queue {queue,job,data,config}.
func (s *Service) QueuePush(queue, job string, data, config any) Result {
	url := s.AppsBaseURL() + "/queue"
	body := map[string]any{"queue": queue, "job": job, "data": data, "config": config}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// QueuePop mirrors apps.queue.pop: GET /apps/queue?queue=...&job=...
func (s *Service) QueuePop(queue, job string) Result {
	url := s.AppsBaseURL() + "/queue"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"queue": queue, "job": job}, nil, 0)
	return s.unwrap(ok, resp)
}

// QueueLog mirrors apps.queue.log: PATCH /apps/queue {jobId,status,output}.
func (s *Service) QueueLog(jobID, logStatus string, output any) Result {
	url := s.AppsBaseURL() + "/queue"
	body := map[string]any{"jobId": jobID, "status": logStatus, "output": output}
	ok, _, _, _, resp := s.client.Internal("PATCH", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.cron ────────────────────────────────────────────────────────────────

// CronRegister mirrors apps.cron.register: POST /apps/cron {group,name,pattern,config}.
func (s *Service) CronRegister(group, name, pattern string, config any) Result {
	url := s.AppsBaseURL() + "/cron"
	body := map[string]any{"group": group, "name": name, "pattern": pattern, "config": config}
	ok, _, _, _, resp := s.client.Internal("POST", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// CronSelect mirrors apps.cron.select: GET /apps/cron?group=...&name=...
func (s *Service) CronSelect(group, name string) Result {
	url := s.AppsBaseURL() + "/cron"
	ok, _, _, _, resp := s.client.Internal("GET", url, nil, map[string]string{"group": group, "name": name}, nil, 0)
	return s.unwrap(ok, resp)
}

// CronLog mirrors apps.cron.log: PATCH /apps/cron {jobId,status,output}.
func (s *Service) CronLog(jobID, logStatus string, output any) Result {
	url := s.AppsBaseURL() + "/cron"
	body := map[string]any{"jobId": jobID, "status": logStatus, "output": output}
	ok, _, _, _, resp := s.client.Internal("PATCH", url, body, nil, nil, 0)
	return s.unwrap(ok, resp)
}

// ── apps.origine ─────────────────────────────────────────────────────────────

// OrigineSourceNamespaceCreate mirrors apps.origine.sourceNamespace.create:
// POST /apps/origine/source-namespace with the raw payload.
func (s *Service) OrigineSourceNamespaceCreate(payload any) Result {
	url := s.AppsBaseURL() + "/origine/source-namespace"
	ok, _, _, _, resp := s.client.Internal("POST", url, payload, nil, nil, 0)
	return s.unwrap(ok, resp)
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
