package idor

import (
	"net/http"
	"testing"
)

// RunSuite is Check wrapped for *testing.T: a CI-friendly go test failure
// per violation found, plus a hard failure if Check itself couldn't run the
// probe at all (e.g. a fixture's Create failed, or the server is
// unreachable). A nil client uses http.DefaultClient.
func RunSuite(t *testing.T, client *http.Client, ownerToken, attackerToken string, fixtures []Fixture) {
	t.Helper()
	violations, err := Check(client, ownerToken, attackerToken, fixtures)
	if err != nil {
		t.Fatalf("idor.Check: %v", err)
	}
	for _, v := range violations {
		t.Errorf("IDOR: %s", v)
	}
}
