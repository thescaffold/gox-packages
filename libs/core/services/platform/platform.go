// Package platform ports ntx-packages/libs/core/src/services/platform.service.ts.
// PlatformService is a thin façade over the core HTTP client that calls
// the standard scaffold app endpoints (controller/identity/capital/common/etc.)
// using HMAC-signed internal authentication.
package platform

import (
	"fmt"
	"strings"

	corehttp "github.com/thescaffold/gox-packages-core/http"
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
