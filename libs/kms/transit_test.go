package kms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeTransit is a transit engine at "transit" with Kubernetes auth at
// "kubernetes": enough of OpenBao's API, with real AES-GCM under a key
// derived from each version's key and the context.
type fakeTransit struct {
	t         *testing.T
	mu        sync.Mutex
	keys      map[string]*fakeKey
	tokens    map[string]bool // valid tokens
	jwt, role string
	namespace string
	logins    int
	sealed    bool
	calls     []string
}

type fakeKey struct {
	derived  bool
	versions [][]byte
	minDec   int
}

func newFakeTransit(t *testing.T) (*fakeTransit, *httptest.Server) {
	f := &fakeTransit{t: t, keys: map[string]*fakeKey{}, tokens: map[string]bool{"root": true}, jwt: "sa-jwt", role: "origine"}
	f.keys["origine"] = &fakeKey{derived: true, versions: [][]byte{key(11)}, minDec: 1}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTransit) rotate(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.keys[name]
	k.versions = append(k.versions, key(byte(11+len(k.versions))))
}

func (f *fakeTransit) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeTransit) fail(w http.ResponseWriter, status int, msg string) {
	f.reply(w, status, map[string]any{"errors": []string{msg}})
}

func derive(k []byte, context string) []byte {
	s := sha256.Sum256(append(append([]byte{}, k...), context...))
	return s[:]
}

func (f *fakeTransit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	f.calls = append(f.calls, r.Method+" "+path)
	if f.namespace != "" && r.Header.Get("X-Vault-Namespace") != f.namespace {
		f.fail(w, http.StatusForbidden, "permission denied")
		return
	}
	if path == "sys/health" {
		if f.sealed {
			f.reply(w, http.StatusServiceUnavailable, map[string]any{"sealed": true})
			return
		}
		f.reply(w, http.StatusOK, map[string]any{"initialized": true})
		return
	}
	var body map[string]string
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if path == "auth/kubernetes/login" {
		if body["role"] != f.role || body["jwt"] != f.jwt {
			f.fail(w, http.StatusForbidden, "permission denied")
			return
		}
		f.logins++
		tok := "k8s-token-" + strconv.Itoa(f.logins)
		f.tokens[tok] = true
		f.reply(w, http.StatusOK, map[string]any{"auth": map[string]any{"client_token": tok, "lease_duration": 3600}})
		return
	}
	if !f.tokens[r.Header.Get("X-Vault-Token")] {
		f.fail(w, http.StatusForbidden, "permission denied")
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] != "transit" {
		f.reply(w, http.StatusNotFound, map[string]any{"errors": []string{}})
		return
	}
	op, name := parts[1], parts[2]
	k := f.keys[name]
	if op == "keys" {
		if k == nil {
			f.reply(w, http.StatusNotFound, map[string]any{"errors": []string{}})
			return
		}
		f.reply(w, http.StatusOK, map[string]any{"data": map[string]any{
			"name": name, "type": "aes256-gcm96", "derived": k.derived, "latest_version": len(k.versions),
			"min_decryption_version": k.minDec, "supports_encryption": true, "supports_decryption": true,
		}})
		return
	}
	if k == nil {
		f.fail(w, http.StatusBadRequest, "encryption key not found")
		return
	}
	ctxb, _ := base64.StdEncoding.DecodeString(body["context"])
	if k.derived && len(ctxb) == 0 {
		f.fail(w, http.StatusBadRequest, "missing 'context' for key derivation")
		return
	}
	keyFor := func(v int) []byte {
		if !k.derived {
			return k.versions[v-1]
		}
		return derive(k.versions[v-1], string(ctxb))
	}
	encrypt := func(pt []byte) map[string]any {
		v := len(k.versions)
		s, err := Seal(keyFor(v), pt, nil)
		if err != nil {
			f.t.Error(err)
		}
		return map[string]any{"data": map[string]any{"ciphertext": fmt.Sprintf("vault:v%d:%s", v, base64.StdEncoding.EncodeToString(s)), "key_version": v}}
	}
	decrypt := func(ct string) ([]byte, string) {
		rest, ok := strings.CutPrefix(ct, "vault:v")
		vs, b64, ok2 := strings.Cut(rest, ":")
		v, err := strconv.Atoi(vs)
		raw, err2 := base64.StdEncoding.DecodeString(b64)
		if !ok || !ok2 || err != nil || err2 != nil || v < 1 || v > len(k.versions) {
			return nil, "invalid ciphertext: unable to decode"
		}
		if v < k.minDec {
			return nil, "ciphertext or signature version is disallowed by policy (too old)"
		}
		pt, err := Open(keyFor(v), raw, nil)
		if err != nil {
			return nil, "cipher: message authentication failed"
		}
		return pt, ""
	}
	switch op {
	case "encrypt":
		pt, _ := base64.StdEncoding.DecodeString(body["plaintext"])
		f.reply(w, http.StatusOK, encrypt(pt))
	case "decrypt":
		pt, msg := decrypt(body["ciphertext"])
		if msg != "" {
			f.fail(w, http.StatusBadRequest, msg)
			return
		}
		f.reply(w, http.StatusOK, map[string]any{"data": map[string]any{"plaintext": base64.StdEncoding.EncodeToString(pt)}})
	case "rewrap":
		pt, msg := decrypt(body["ciphertext"])
		if msg != "" {
			f.fail(w, http.StatusBadRequest, msg)
			return
		}
		f.reply(w, http.StatusOK, encrypt(pt))
	default:
		f.reply(w, http.StatusNotFound, map[string]any{"errors": []string{}})
	}
}

func (f *fakeTransit) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestTransitWrapUnwrapAndRotation(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeTransit(t)
	tr, err := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", Token: "root"})
	if err != nil {
		t.Fatal(err)
	}
	dek, _ := NewDataKey()
	id, wrapped, err := tr.Wrap(ctx, dek, []byte("ws-1"))
	if err != nil || id != "transit:transit/origine" || WrappedVersion(wrapped) != 1 {
		t.Fatalf("wrap: %q %q %v", id, wrapped, err)
	}
	if got, err := tr.Unwrap(ctx, id, wrapped, []byte("ws-1")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("unwrap: %v", err)
	}
	if _, err := tr.Unwrap(ctx, id, wrapped, []byte("ws-2")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other aad unwrapped: %v", err)
	}
	if _, err := tr.Unwrap(ctx, id, wrapped, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("empty aad unwrapped: %v", err)
	}
	if _, err := tr.Unwrap(ctx, "local:k1", wrapped, nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a local key id: %v", err)
	}
	if _, err := tr.Unwrap(ctx, "transit:transit/gone", wrapped, []byte("ws-1")); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an unknown key: %v", err)
	}
	if _, err := tr.Unwrap(ctx, id, []byte("not-transit"), []byte("ws-1")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("garbage: %v", err)
	}
	// the key was read once, for the first wrap only
	if n := f.count("GET transit/keys/origine"); n != 1 {
		t.Fatalf("key reads: %d", n)
	}

	// rotate: new wraps use v2, v1 still unwraps, rewrap moves v1 to v2 in the server
	f.rotate("origine")
	_, w2, err := tr.Wrap(ctx, dek, []byte("ws-1"))
	if err != nil || WrappedVersion(w2) != 2 {
		t.Fatalf("wrap after rotation: %q %v", w2, err)
	}
	if got, err := tr.Unwrap(ctx, id, wrapped, []byte("ws-1")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("v1 after rotation: %v", err)
	}
	decrypts := f.count("POST transit/decrypt/")
	id2, rw, err := Rewrap(ctx, tr, id, wrapped, []byte("ws-1"))
	if err != nil || id2 != id || WrappedVersion(rw) != 2 {
		t.Fatalf("rewrap: %q %q %v", id2, rw, err)
	}
	if f.count("POST transit/decrypt/") != decrypts || f.count("POST transit/rewrap/origine") != 1 {
		t.Fatal("rewrap must run in the server, not through decrypt")
	}
	if _, _, err := Rewrap(ctx, tr, id, wrapped, []byte("ws-2")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("rewrap with other aad: %v", err)
	}
	// retire v1: the rewrapped key still unwraps, the old one does not
	f.mu.Lock()
	f.keys["origine"].minDec = 2
	f.mu.Unlock()
	if got, err := tr.Unwrap(ctx, id2, rw, []byte("ws-1")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("rewrapped after retiring v1: %v", err)
	}
	if _, err := tr.Unwrap(ctx, id, wrapped, []byte("ws-1")); err == nil {
		t.Fatal("v1 unwrapped after it was retired")
	}

	// the probe admin check runs
	if pid, err := Probe(ctx, tr); err != nil || pid != id {
		t.Fatalf("probe: %q %v", pid, err)
	}
	k, err := tr.Key(ctx)
	if err != nil || k.LatestVersion != 2 || !k.Derived || k.Usable() != nil {
		t.Fatalf("key: %+v %v", k, err)
	}
}

func TestTransitRefusesAKeyThatIsNotDerived(t *testing.T) {
	f, srv := newFakeTransit(t)
	f.keys["plain"] = &fakeKey{versions: [][]byte{key(3)}, minDec: 1}
	tr, _ := NewTransit(TransitConfig{Address: srv.URL, Key: "plain", Token: "root"})
	if _, _, err := tr.Wrap(context.Background(), key(1), []byte("ws")); !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "derived=true") {
		t.Fatalf("not derived: %v", err)
	}
	tr, _ = NewTransit(TransitConfig{Address: srv.URL, Key: "missing", Token: "root"})
	if _, _, err := tr.Wrap(context.Background(), key(1), nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("missing key: %v", err)
	}
	if _, err := tr.Key(context.Background()); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("missing key read: %v", err)
	}
}

func TestTransitKubernetesAuth(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeTransit(t)
	jwt := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(jwt, []byte("sa-jwt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", KubernetesRole: "origine", KubernetesTokenFile: jwt})
	if err != nil {
		t.Fatal(err)
	}
	dek, _ := NewDataKey()
	id, wrapped, err := tr.Wrap(ctx, dek, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Unwrap(ctx, id, wrapped, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Fatalf("logged in %d times, want once", f.logins)
	}
	// the token is revoked: one fresh login, then the request succeeds
	f.mu.Lock()
	f.tokens = map[string]bool{}
	f.mu.Unlock()
	if _, err := tr.Unwrap(ctx, id, wrapped, []byte("a")); err != nil || f.logins != 2 {
		t.Fatalf("after revocation: %v, %d logins", err, f.logins)
	}
	// a wrong role is ErrDenied, at login
	bad, _ := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", KubernetesRole: "other", KubernetesTokenFile: jwt})
	if err := bad.Login(ctx); !errors.Is(err, ErrDenied) {
		t.Fatalf("wrong role: %v", err)
	}
	var te *TransitError
	if _, _, err := bad.Wrap(ctx, dek, nil); !errors.As(err, &te) || te.Op != "login" {
		t.Fatalf("wrap with a wrong role fails at login: %v", err)
	}
	missing, _ := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", KubernetesRole: "origine", KubernetesTokenFile: jwt + ".none"})
	if err := missing.Login(ctx); !errors.Is(err, ErrConfig) {
		t.Fatalf("no service account token: %v", err)
	}
}

func TestTransitTokenFileNamespaceAndHealth(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeTransit(t)
	f.namespace = "team-a"
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("root"), 0o600)
	tr, err := NewTransit(TransitConfig{Address: srv.URL + "/", Mount: "/transit/", Key: "origine", TokenFile: tok, Namespace: "team-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(ctx, tr); err != nil {
		t.Fatal(err)
	}
	if err := tr.Health(ctx); err != nil {
		t.Fatal(err)
	}
	other, _ := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", Token: "root"})
	if _, _, err := other.Wrap(ctx, key(1), nil); !errors.Is(err, ErrDenied) {
		t.Fatalf("without the namespace: %v", err)
	}
	wrong, _ := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", Token: "nope", Namespace: "team-a"})
	if _, _, err := wrong.Wrap(ctx, key(1), nil); !errors.Is(err, ErrDenied) {
		t.Fatalf("a wrong token: %v", err)
	}
	f.mu.Lock()
	f.sealed = true
	f.mu.Unlock()
	if err := tr.Health(ctx); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("sealed: %v", err)
	}
	srv.Close()
	if err := tr.Health(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
	if _, err := tr.Unwrap(ctx, tr.KeyID(), []byte("vault:v1:AAAA"), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down unwrap: %v", err)
	}
}

func TestTransitConfig(t *testing.T) {
	for name, cfg := range map[string]TransitConfig{
		"no address":    {Key: "k", Token: "t"},
		"not a URL":     {Address: "openbao:8200", Key: "k", Token: "t"},
		"no key":        {Address: "https://bao", Token: "t"},
		"a key path":    {Address: "https://bao", Key: "a/b", Token: "t"},
		"no auth":       {Address: "https://bao", Key: "k"},
		"two auths":     {Address: "https://bao", Key: "k", Token: "t", KubernetesRole: "r"},
		"a missing CA":  {Address: "https://bao", Key: "k", Token: "t", CAFile: "/nonexistent/ca.pem"},
		"a CA, not PEM": {Address: "https://bao", Key: "k", Token: "t", CAFile: "transit_test.go"},
	} {
		if _, err := NewTransit(cfg); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tr, err := NewTransit(TransitConfig{Address: "https://bao:8200", Key: "k", KubernetesRole: "r", Namespace: "ns"})
	if err != nil {
		t.Fatal(err)
	}
	if s := tr.String(); !strings.Contains(s, "https://bao:8200") || !strings.Contains(s, "Kubernetes role r") || strings.Contains(s, "token") {
		t.Fatalf("String: %q", s)
	}
	if WrappedVersion([]byte("vault:v12:abc")) != 12 || WrappedVersion([]byte("local")) != 0 {
		t.Fatal("WrappedVersion")
	}
}

func TestChainMovesLocalToTransit(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeTransit(t)
	local, _ := NewLocal(map[string][]byte{"k1": key(1)}, "k1")
	dek, _ := NewDataKey()
	lid, lw, _ := local.Wrap(ctx, dek, []byte("ws"))

	tr, _ := NewTransit(TransitConfig{Address: srv.URL, Key: "origine", Token: "root"})
	c := Chain{tr, local}
	if got, err := c.Unwrap(ctx, lid, lw, []byte("ws")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("local key through the chain: %v", err)
	}
	if _, err := c.Unwrap(ctx, lid, lw, []byte("other")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other aad through the chain: %v", err)
	}
	id, w, err := Rewrap(ctx, c, lid, lw, []byte("ws"))
	if err != nil || id != tr.KeyID() {
		t.Fatalf("local to transit: %q %v", id, err)
	}
	if got, err := tr.Unwrap(ctx, id, w, []byte("ws")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("moved key: %v", err)
	}
	f.rotate("origine")
	if _, w3, err := Rewrap(ctx, c, id, w, []byte("ws")); err != nil || WrappedVersion(w3) != 2 || f.count("POST transit/rewrap/") != 1 {
		t.Fatalf("transit key through the chain is rewrapped in the server: %v", err)
	}
	if _, err := c.Unwrap(ctx, "aws:x", nil, nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown driver: %v", err)
	}
	// Local rewraps through unwrap and wrap, to its current key
	rotated, _ := NewLocal(map[string][]byte{"k1": key(1), "k2": key(2)}, "k2")
	if id, _, err := Rewrap(ctx, rotated, lid, lw, []byte("ws")); err != nil || id != "local:k2" {
		t.Fatalf("local rewrap: %q %v", id, err)
	}
	if _, err := Probe(ctx, rotated); err != nil {
		t.Fatalf("local probe: %v", err)
	}
}

func TestFromEnvTransit(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := FromEnv(env(map[string]string{"KMS_DRIVER": "transit"})); !errors.Is(err, ErrConfig) {
		t.Fatalf("no address: %v", err)
	}
	if _, err := FromEnv(env(map[string]string{"KMS_DRIVER": "transit", "KMS_TRANSIT_ADDR": "https://bao"})); !errors.Is(err, ErrConfig) {
		t.Fatalf("no key: %v", err)
	}
	m := map[string]string{"KMS_DRIVER": "Transit", "KMS_TRANSIT_ADDR": "https://bao:8200", "KMS_TRANSIT_KEY": "origine", "KMS_TRANSIT_KUBERNETES_ROLE": "origine"}
	a, err := FromEnv(env(m))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FromEnv(env(m))
	if _, ok := a.(*Transit); !ok || a != b {
		t.Fatalf("one shared transit client: %T %p %p", a, a, b)
	}
	if Driver(env(m)) != "transit" || Driver(env(nil)) != "local" {
		t.Fatal("Driver")
	}
	m["KMS_LOCAL_KEYS"] = "k1:" + base64.StdEncoding.EncodeToString(key(1))
	c, err := FromEnv(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if ch, ok := c.(Chain); !ok || len(ch) != 2 || ch[0] != a {
		t.Fatalf("transit with local keys is a chain: %T", c)
	}
}

// An OpenBao in Docker, e.g.
//
//	docker run -d -p 127.0.0.1:8200:8200 -e BAO_DEV_ROOT_TOKEN_ID=root openbao/openbao server -dev -dev-listen-address=0.0.0.0:8200
//	KMS_TEST_TRANSIT_ADDR=http://127.0.0.1:8200 KMS_TEST_TRANSIT_TOKEN=root go test -run Integration ./...
//
// The test mounts transit at a fresh path, so it can run against any dev
// server; the token needs sys/mounts and the mount's paths.
func TestTransitIntegration(t *testing.T) {
	addr, root := os.Getenv("KMS_TEST_TRANSIT_ADDR"), os.Getenv("KMS_TEST_TRANSIT_TOKEN")
	if addr == "" || root == "" {
		t.Skip("set KMS_TEST_TRANSIT_ADDR and KMS_TEST_TRANSIT_TOKEN to run against OpenBao or Vault")
	}
	ctx := context.Background()
	mount := fmt.Sprintf("kms-test-%d", os.Getpid())
	call := func(method, path string, body any) {
		t.Helper()
		var rd *bytes.Reader
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
		req, _ := http.NewRequest(method, strings.TrimRight(addr, "/")+"/v1/"+path, rd)
		req.Header.Set("X-Vault-Token", root)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode >= 300 {
			var e map[string]any
			_ = json.NewDecoder(res.Body).Decode(&e)
			t.Fatalf("%s %s: %d %v", method, path, res.StatusCode, e)
		}
	}
	call(http.MethodPost, "sys/mounts/"+mount, map[string]any{"type": "transit"})
	t.Cleanup(func() { call(http.MethodDelete, "sys/mounts/"+mount, nil) })
	call(http.MethodPost, mount+"/keys/origine", map[string]any{"derived": true})
	call(http.MethodPost, mount+"/keys/plain", map[string]any{})

	tr, err := NewTransit(TransitConfig{Address: addr, Mount: mount, Key: "origine", Token: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Health(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(ctx, tr); err != nil {
		t.Fatal(err)
	}
	dek, _ := NewDataKey()
	id, w1, err := tr.Wrap(ctx, dek, []byte("ws-1"))
	if err != nil || WrappedVersion(w1) != 1 {
		t.Fatalf("wrap: %v", err)
	}
	if _, err := tr.Unwrap(ctx, id, w1, []byte("ws-2")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("other aad: %v", err)
	}
	call(http.MethodPost, mount+"/keys/origine/rotate", map[string]any{})
	_, w2, err := tr.Wrap(ctx, dek, []byte("ws-1"))
	if err != nil || WrappedVersion(w2) != 2 {
		t.Fatalf("wrap after rotation: %q %v", w2, err)
	}
	if got, err := tr.Unwrap(ctx, id, w1, []byte("ws-1")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("v1 after rotation: %v", err)
	}
	_, rw, err := Rewrap(ctx, tr, id, w1, []byte("ws-1"))
	if err != nil || WrappedVersion(rw) != 2 {
		t.Fatalf("rewrap: %q %v", rw, err)
	}
	if _, _, err := Rewrap(ctx, tr, id, w1, []byte("ws-2")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("rewrap with other aad: %v", err)
	}
	call(http.MethodPost, mount+"/keys/origine/config", map[string]any{"min_decryption_version": 2})
	if got, err := tr.Unwrap(ctx, id, rw, []byte("ws-1")); err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("rewrapped after retiring v1: %v", err)
	}
	if _, err := tr.Unwrap(ctx, id, w1, []byte("ws-1")); err == nil {
		t.Fatal("v1 unwrapped after it was retired")
	}
	k, err := tr.Key(ctx)
	if err != nil || k.LatestVersion != 2 || k.MinDecryptionVersion != 2 || k.Usable() != nil {
		t.Fatalf("key: %+v %v", k, err)
	}
	plain, _ := NewTransit(TransitConfig{Address: addr, Mount: mount, Key: "plain", Token: root})
	if _, _, err := plain.Wrap(ctx, dek, []byte("ws-1")); !errors.Is(err, ErrConfig) {
		t.Fatalf("a key that is not derived: %v", err)
	}
	missing, _ := NewTransit(TransitConfig{Address: addr, Mount: mount, Key: "missing", Token: root})
	if _, err := missing.Key(ctx); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a missing key: %v", err)
	}
	denied, _ := NewTransit(TransitConfig{Address: addr, Mount: mount, Key: "origine", Token: "not-a-token"})
	if _, err := denied.Unwrap(ctx, id, rw, []byte("ws-1")); !errors.Is(err, ErrDenied) {
		t.Fatalf("a wrong token: %v", err)
	}
}
