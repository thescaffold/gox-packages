package tests

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/core/response"
)

// An error can carry machine-readable details beside its code (lane 3: wallet packed remaining
// into the message text); an error without them sends no details key, as before.
func TestErrorDetails(t *testing.T) {
	out := response.ErrorDetails("wallet", "over the limit", http.StatusUnprocessableEntity, "limit_exceeded", map[string]any{"remaining": "1200"})
	b, _ := json.Marshal(out.Data())
	var got struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(b, &got)
	if out.Code() != 422 || got.Code != "limit_exceeded" || got.Details["remaining"] != "1200" {
		t.Fatalf("ErrorDetails = %d %s", out.Code(), b)
	}
	b, _ = json.Marshal(response.ErrorCode("wallet", "x", 400, "bad").Data())
	if strings.Contains(string(b), `"details"`) {
		t.Fatalf("an error without details has a details key: %s", b)
	}
}
