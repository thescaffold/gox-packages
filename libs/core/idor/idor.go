// Package idor is a reusable tenancy/IDOR test harness (PLAN M1-09, TRD
// §7.1): given a Fixture per tenant-scoped resource — how to create a valid
// row as one workspace's token, and the resource's base URL — Check replays
// the exact live repro PLAN M1-02a's own fix was verified against: create a
// row as the owner, then attempt to List/Get/Update/Delete it as a second,
// unrelated token. Every gox-app CRUD response shares the same envelope
// shape (response.Envelope), so this needs no other per-app knowledge —
// only Create varies per resource, since required fields differ.
package idor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Fixture describes one tenant-scoped resource to probe.
type Fixture struct {
	// Name identifies the fixture in a Violation.
	Name string
	// BasePath is the resource's list/create URL, e.g.
	// "http://127.0.0.1:8140/api/apps/systems/project" — Get/Update/Delete
	// URLs are formed by appending "/"+id.
	BasePath string
	// Create posts a valid create payload as the given bearer token and
	// returns the created row's id (read from the response envelope's
	// data.id). Each fixture supplies its own body since required Create
	// fields vary per resource — everything downstream of Create is generic.
	Create func(client *http.Client, basePath, token string) (id string, err error)
}

// Violation is one specific leak (or regression) Check found.
type Violation struct {
	Fixture string
	// Check is one of "list", "get", "update", "delete" (a leak: the
	// attacker's token reached the owner's row) or "owner-regression" (the
	// owner's own token was wrongly rejected after the probes above).
	Check  string
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s/%s: %s", v.Fixture, v.Check, v.Detail)
}

// envelope mirrors gox-packages/core/response.Envelope's wire shape, kept
// local (rather than importing response) so this package has no dependency
// beyond net/http and encoding/json and can probe any server speaking this
// envelope shape, gox-app or not.
type envelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// Check replays IDOR probes for each fixture against a live server: create a
// row as ownerToken, then attempt to List/Get/Update/Delete it as
// attackerToken, and finally confirm ownerToken's own access still works
// afterward. Returns every violation found — an empty slice with a nil error
// means every fixture correctly rejected the attacker's token (and left the
// owner's own access intact). A non-nil error means the harness itself
// couldn't run the probe (e.g. Create failed, or the server is unreachable)
// — distinct from a Violation, which means the probe ran and found a leak.
func Check(client *http.Client, ownerToken, attackerToken string, fixtures []Fixture) ([]Violation, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var violations []Violation

	for _, f := range fixtures {
		if f.BasePath == "" || f.Create == nil {
			return nil, fmt.Errorf("fixture %q: BasePath and Create are required", f.Name)
		}

		id, err := f.Create(client, f.BasePath, ownerToken)
		if err != nil {
			return nil, fmt.Errorf("fixture %q: create as owner: %w", f.Name, err)
		}
		if id == "" {
			return nil, fmt.Errorf("fixture %q: create as owner returned an empty id", f.Name)
		}
		itemURL := strings.TrimRight(f.BasePath, "/") + "/" + id

		if detail, err := probeList(client, f.BasePath, attackerToken, id); err != nil {
			return nil, fmt.Errorf("fixture %q: list probe: %w", f.Name, err)
		} else if detail != "" {
			violations = append(violations, Violation{Fixture: f.Name, Check: "list", Detail: detail})
		}

		if detail, err := probeAccess(client, http.MethodGet, itemURL, attackerToken, id, true); err != nil {
			return nil, fmt.Errorf("fixture %q: get probe: %w", f.Name, err)
		} else if detail != "" {
			violations = append(violations, Violation{Fixture: f.Name, Check: "get", Detail: detail})
		}

		if detail, err := probeMutate(client, http.MethodPatch, itemURL, attackerToken); err != nil {
			return nil, fmt.Errorf("fixture %q: update probe: %w", f.Name, err)
		} else if detail != "" {
			violations = append(violations, Violation{Fixture: f.Name, Check: "update", Detail: detail})
		}

		// Sanity: the owner's own access must still work at this point — a
		// scoping fix that's so aggressive it locks out the legitimate
		// owner too is exactly as real a bug as a leak, and a harness that
		// can't tell the difference would eventually get "fixed" by
		// disabling scoping altogether. Checked *before* the delete probe
		// below, which is destructive — on a genuinely leaky resource the
		// attacker's delete would succeed and remove the row, which would
		// otherwise make the owner look locked out for a completely
		// different (and already-reported, via the "delete" violation)
		// reason.
		if detail, err := probeAccess(client, http.MethodGet, itemURL, ownerToken, id, false); err != nil {
			return nil, fmt.Errorf("fixture %q: owner regression probe: %w", f.Name, err)
		} else if detail != "" {
			violations = append(violations, Violation{Fixture: f.Name, Check: "owner-regression", Detail: detail})
		}

		if detail, err := probeMutate(client, http.MethodDelete, itemURL, attackerToken); err != nil {
			return nil, fmt.Errorf("fixture %q: delete probe: %w", f.Name, err)
		} else if detail != "" {
			violations = append(violations, Violation{Fixture: f.Name, Check: "delete", Detail: detail})
		}
	}

	return violations, nil
}

// probeList GETs basePath as token and reports a violation if ownerRowID
// appears anywhere in the returned list.
func probeList(client *http.Client, basePath, token, ownerRowID string) (string, error) {
	env, status, err := doJSON(client, http.MethodGet, basePath, token, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || env.Status != "success" {
		return "", nil // rejected outright — safe
	}
	var rows []map[string]any
	if err := json.Unmarshal(env.Data, &rows); err != nil {
		// Not a list-shaped success body — nothing to compare against.
		return "", nil
	}
	for _, row := range rows {
		if idOf(row) == ownerRowID {
			return fmt.Sprintf("attacker's token listed the owner's row (id=%s) at %s", ownerRowID, basePath), nil
		}
	}
	return "", nil
}

// probeAccess GETs itemURL as token. wantRejected controls interpretation:
// true (an attacker probe) flags a violation if the request *succeeds* and
// returns the owner's row; false (an owner sanity probe) flags a violation
// if the request is rejected.
func probeAccess(client *http.Client, method, itemURL, token, ownerRowID string, wantRejected bool) (string, error) {
	env, status, err := doJSON(client, method, itemURL, token, nil)
	if err != nil {
		return "", err
	}
	succeeded := status == http.StatusOK && env.Status == "success"
	if wantRejected {
		if succeeded {
			return fmt.Sprintf("attacker's token fetched the owner's row (id=%s) at %s (HTTP %d)", ownerRowID, itemURL, status), nil
		}
		return "", nil
	}
	if !succeeded {
		return fmt.Sprintf("owner's own token was rejected at %s (HTTP %d, status=%q)", itemURL, status, env.Status), nil
	}
	return "", nil
}

// probeMutate sends method (PATCH or DELETE) to itemURL as token and flags a
// violation if it succeeds — the attacker's token should never be able to
// mutate a row it doesn't own.
func probeMutate(client *http.Client, method, itemURL, token string) (string, error) {
	var body io.Reader
	if method == http.MethodPatch {
		body = strings.NewReader(`{}`)
	}
	env, status, err := doJSON(client, method, itemURL, token, body)
	if err != nil {
		return "", err
	}
	if status == http.StatusOK && env.Status == "success" {
		return fmt.Sprintf("attacker's token %s succeeded on the owner's row at %s (HTTP %d)", method, itemURL, status), nil
	}
	return "", nil
}

func doJSON(client *http.Client, method, url, token string, body io.Reader) (envelope, int, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return envelope{}, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return envelope{}, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return envelope{}, resp.StatusCode, err
	}
	var env envelope
	// A non-JSON body (e.g. a platform-level 404/405 page) just means
	// env.Status stays "" — treated as "not a success", which is correct.
	_ = json.Unmarshal(raw, &env)
	return env, resp.StatusCode, nil
}

func idOf(row map[string]any) string {
	if row == nil {
		return ""
	}
	if v, ok := row["id"].(string); ok {
		return v
	}
	return ""
}
