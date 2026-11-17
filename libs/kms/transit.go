package kms

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultKubernetesTokenFile is where a pod's service account token is mounted.
const DefaultKubernetesTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// TransitConfig configures a Transit KMS.
type TransitConfig struct {
	// Address of the server, e.g. https://openbao.openbao.svc:8200.
	Address string
	// Mount is the transit engine's path (default "transit").
	Mount string
	// Key is the name of the transit key new wraps use.
	Key string
	// Namespace is sent as X-Vault-Namespace (Vault Enterprise and OpenBao namespaces).
	Namespace string

	// Authentication: a token (Token, or TokenFile read on each request), or
	// Kubernetes auth (KubernetesRole, logging in with the service account's
	// JWT from KubernetesTokenFile at auth/KubernetesMount).
	Token               string
	TokenFile           string
	KubernetesRole      string
	KubernetesMount     string // default "kubernetes"
	KubernetesTokenFile string // default DefaultKubernetesTokenFile

	// CAFile is a PEM bundle the server's certificate must chain to (default:
	// the system's roots).
	CAFile string
	// Timeout of each request (default 10s). Ignored when HTTPClient is set.
	Timeout time.Duration
	// HTTPClient replaces the client built from CAFile and Timeout (tests).
	HTTPClient *http.Client
}

// Transit is a KMS backed by the transit secrets engine of OpenBao or
// HashiCorp Vault (the same HTTP API). The key encryption key never leaves the
// server: Wrap calls encrypt and Unwrap calls decrypt.
//
// aad is bound through key derivation: the key must be created with
// derived=true, and each request sends "kms\x00" + aad as the transit context,
// so a wrapped key decrypts only under the context it was wrapped with, as
// Local's AES-GCM aad does. (Transit's associated_data would bind it too, but
// rewrap does not take it, so rotated keys could not be rewrapped in the
// server.) Wrap refuses a key that is not derived: transit ignores the context
// of a key that is not, and the binding would be silently lost.
//
// Rotation happens in the server (POST <mount>/keys/<key>/rotate): new wraps
// use the newest version, older versions still unwrap, and Rewrap moves a
// wrapped key to the newest version without the data key leaving the server.
type Transit struct {
	cfg  TransitConfig
	base string
	hc   *http.Client

	mu         sync.Mutex
	token      string
	tokenUntil time.Time // zero: no known expiry
	keyChecked bool      // the key was read and is derived
}

var (
	_ KMS       = (*Transit)(nil)
	_ Rewrapper = (*Transit)(nil)
)

// NewTransit validates cfg and builds a Transit KMS. It makes no request.
func NewTransit(cfg TransitConfig) (*Transit, error) {
	cfg.Address = strings.TrimRight(strings.TrimSpace(cfg.Address), "/")
	u, err := url.Parse(cfg.Address)
	if cfg.Address == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("%w: the transit address %q is not an http(s) URL", ErrConfig, cfg.Address)
	}
	cfg.Mount = strings.Trim(strings.TrimSpace(cfg.Mount), "/")
	if cfg.Mount == "" {
		cfg.Mount = "transit"
	}
	if err := checkTransitName("key", cfg.Key); err != nil {
		return nil, err
	}
	cfg.KubernetesMount = strings.Trim(strings.TrimSpace(cfg.KubernetesMount), "/")
	if cfg.KubernetesMount == "" {
		cfg.KubernetesMount = "kubernetes"
	}
	if cfg.KubernetesTokenFile == "" {
		cfg.KubernetesTokenFile = DefaultKubernetesTokenFile
	}
	methods := 0
	for _, set := range []bool{cfg.Token != "", cfg.TokenFile != "", cfg.KubernetesRole != ""} {
		if set {
			methods++
		}
	}
	if methods != 1 {
		return nil, fmt.Errorf("%w: transit needs exactly one of a token, a token file or a Kubernetes role", ErrConfig)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.CAFile != "" {
			pem, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("%w: the transit CA file: %v", ErrConfig, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("%w: the transit CA file %s holds no PEM certificate", ErrConfig, cfg.CAFile)
			}
			tr.TLSClientConfig.RootCAs = pool
		}
		hc = &http.Client{Timeout: timeout, Transport: tr}
	}
	return &Transit{cfg: cfg, base: cfg.Address + "/v1/", hc: hc}, nil
}

func checkTransitName(what, name string) error {
	if name == "" || strings.ContainsAny(name, "/:, ?#") {
		return fmt.Errorf("%w: transit %s %q", ErrConfig, what, name)
	}
	return nil
}

// KeyID is the id Wrap returns: "transit:<mount>/<key>".
func (t *Transit) KeyID() string { return "transit:" + t.cfg.Mount + "/" + t.cfg.Key }

// String names the server and key, never a credential.
func (t *Transit) String() string {
	auth := "a token"
	if t.cfg.KubernetesRole != "" {
		auth = "Kubernetes role " + t.cfg.KubernetesRole
	}
	s := fmt.Sprintf("transit at %s, key %s/%s, %s", t.cfg.Address, t.cfg.Mount, t.cfg.Key, auth)
	if t.cfg.Namespace != "" {
		s += ", namespace " + t.cfg.Namespace
	}
	return s
}

// parseKeyID splits "transit:<mount>/<key>".
func parseTransitKeyID(keyID string) (mount, key string, ok bool) {
	rest, ok := strings.CutPrefix(keyID, "transit:")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndexByte(rest, '/')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

func (t *Transit) Wrap(ctx context.Context, dek, aad []byte) (string, []byte, error) {
	if err := t.checkKey(ctx); err != nil {
		return "", nil, err
	}
	body := map[string]any{"plaintext": base64.StdEncoding.EncodeToString(dek), "context": transitContext(aad)}
	var out struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := t.do(ctx, "encrypt", http.MethodPost, t.cfg.Mount+"/encrypt/"+t.cfg.Key, body, &out); err != nil {
		return "", nil, err
	}
	if !strings.HasPrefix(out.Ciphertext, "vault:v") {
		return "", nil, &TransitError{Op: "encrypt", Messages: []string{"the server returned no ciphertext"}, kind: ErrUnavailable}
	}
	return t.KeyID(), []byte(out.Ciphertext), nil
}

func (t *Transit) Unwrap(ctx context.Context, keyID string, wrapped, aad []byte) ([]byte, error) {
	mount, key, ok := parseTransitKeyID(keyID)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a transit key", ErrUnknownKey, keyID)
	}
	if !strings.HasPrefix(string(wrapped), "vault:v") {
		return nil, ErrDecrypt
	}
	body := map[string]any{"ciphertext": string(wrapped), "context": transitContext(aad)}
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	if err := t.do(ctx, "decrypt", http.MethodPost, mount+"/decrypt/"+key, body, &out); err != nil {
		return nil, err
	}
	dek, err := base64.StdEncoding.DecodeString(out.Plaintext)
	if err != nil {
		return nil, ErrDecrypt
	}
	return dek, nil
}

// Rewrap re-encrypts a wrapped key under the newest version of the
// configured key, inside the server (transit's rewrap): the data key is never
// returned. A key wrapped under another transit key, or by another driver,
// is unwrapped and wrapped again instead.
func (t *Transit) Rewrap(ctx context.Context, keyID string, wrapped, aad []byte) (string, []byte, error) {
	if keyID != t.KeyID() {
		return rewrapThrough(ctx, t, keyID, wrapped, aad)
	}
	if err := t.checkKey(ctx); err != nil {
		return "", nil, err
	}
	body := map[string]any{"ciphertext": string(wrapped), "context": transitContext(aad)}
	var out struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := t.do(ctx, "rewrap", http.MethodPost, t.cfg.Mount+"/rewrap/"+t.cfg.Key, body, &out); err != nil {
		return "", nil, err
	}
	if !strings.HasPrefix(out.Ciphertext, "vault:v") {
		return "", nil, &TransitError{Op: "rewrap", Messages: []string{"the server returned no ciphertext"}, kind: ErrUnavailable}
	}
	return t.KeyID(), []byte(out.Ciphertext), nil
}

// transitContext is the derivation context for aad. It is never empty, as a
// derived key requires one.
func transitContext(aad []byte) string {
	return base64.StdEncoding.EncodeToString(append([]byte("kms\x00"), aad...))
}

// checkKey reads the key once per process and refuses one Wrap cannot bind
// aad with.
func (t *Transit) checkKey(ctx context.Context) error {
	t.mu.Lock()
	ok := t.keyChecked
	t.mu.Unlock()
	if ok {
		return nil
	}
	k, err := t.Key(ctx)
	if err != nil {
		return err
	}
	if err := k.Usable(); err != nil {
		return err
	}
	t.mu.Lock()
	t.keyChecked = true
	t.mu.Unlock()
	return nil
}

// WrappedVersion is the key version a transit-wrapped key was made with
// ("vault:v3:…" is 3), or 0 when wrapped is not transit's.
func WrappedVersion(wrapped []byte) int {
	rest, ok := strings.CutPrefix(string(wrapped), "vault:v")
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(rest[:max(strings.IndexByte(rest, ':'), 0)])
	return n
}

// Health checks that the server answers and is initialized and unsealed
// (sys/health, which needs no token).
func (t *Transit) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+"sys/health?standbyok=true&perfstandbyok=true", nil)
	if err != nil {
		return err
	}
	t.header(req, "")
	res, err := t.hc.Do(req)
	if err != nil {
		return &TransitError{Op: "health", Messages: []string{err.Error()}, kind: ErrUnavailable}
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	switch res.StatusCode {
	case http.StatusOK, http.StatusTooManyRequests, 472, 473:
		return nil
	case http.StatusNotImplemented:
		return &TransitError{Op: "health", Status: res.StatusCode, Messages: []string{"the server is not initialized"}, kind: ErrUnavailable}
	case http.StatusServiceUnavailable:
		return &TransitError{Op: "health", Status: res.StatusCode, Messages: []string{"the server is sealed"}, kind: ErrUnavailable}
	default:
		return &TransitError{Op: "health", Status: res.StatusCode, Messages: []string{res.Status}, kind: ErrUnavailable}
	}
}

// TransitKey describes the configured key (<mount>/keys/<key>).
type TransitKey struct {
	Name                 string `json:"name"`
	Type                 string `json:"type"`
	LatestVersion        int    `json:"latest_version"`
	MinDecryptionVersion int    `json:"min_decryption_version"`
	MinEncryptionVersion int    `json:"min_encryption_version"`
	Derived              bool   `json:"derived"`
	SupportsEncryption   bool   `json:"supports_encryption"`
	SupportsDecryption   bool   `json:"supports_decryption"`
}

// Usable says why Transit cannot wrap with this key, or nil.
func (k TransitKey) Usable() error {
	switch {
	case !k.SupportsEncryption || !k.SupportsDecryption:
		return fmt.Errorf("%w: the transit key %s (%s) cannot encrypt and decrypt", ErrConfig, k.Name, k.Type)
	case !k.Derived:
		return fmt.Errorf("%w: the transit key %s is not derived, so wrapped keys could not be bound to their context: create it with derived=true", ErrConfig, k.Name)
	}
	return nil
}

// Key reads the configured key; a missing key is ErrUnknownKey. The token's
// policy needs read on <mount>/keys/<key>.
func (t *Transit) Key(ctx context.Context) (TransitKey, error) {
	var k TransitKey
	err := t.do(ctx, "read key", http.MethodGet, t.cfg.Mount+"/keys/"+t.cfg.Key, nil, &k)
	if err == nil && k.Name == "" {
		k.Name = t.cfg.Key
	}
	return k, err
}

// Login authenticates now (with Kubernetes auth, a fresh login), so a caller
// can tell a failed login apart from a failed request.
func (t *Transit) Login(ctx context.Context) error {
	t.mu.Lock()
	t.token = ""
	t.mu.Unlock()
	_, err := t.authToken(ctx)
	return err
}

func (t *Transit) header(req *http.Request, token string) {
	if t.cfg.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", t.cfg.Namespace)
	}
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	req.Header.Set("X-Vault-Request", "true")
}

// authToken returns the token to send.
func (t *Transit) authToken(ctx context.Context) (string, error) {
	switch {
	case t.cfg.Token != "":
		return t.cfg.Token, nil
	case t.cfg.TokenFile != "":
		b, err := os.ReadFile(t.cfg.TokenFile)
		if err != nil {
			return "", &TransitError{Op: "login", Messages: []string{"the token file: " + err.Error()}, kind: ErrConfig}
		}
		return strings.TrimSpace(string(b)), nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && (t.tokenUntil.IsZero() || time.Now().Before(t.tokenUntil)) {
		return t.token, nil
	}
	jwt, err := os.ReadFile(t.cfg.KubernetesTokenFile)
	if err != nil {
		return "", &TransitError{Op: "login", Messages: []string{"the service account token: " + err.Error()}, kind: ErrConfig}
	}
	body := map[string]any{"role": t.cfg.KubernetesRole, "jwt": strings.TrimSpace(string(jwt))}
	var out struct {
		Auth *struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := t.send(ctx, "login", http.MethodPost, "auth/"+t.cfg.KubernetesMount+"/login", "", body, &out, false); err != nil {
		return "", err
	}
	if out.Auth == nil || out.Auth.ClientToken == "" {
		return "", &TransitError{Op: "login", Messages: []string{"the server returned no token"}, kind: ErrDenied}
	}
	t.token = out.Auth.ClientToken
	t.tokenUntil = time.Time{}
	if out.Auth.LeaseDuration > 0 {
		// log in again well before the token expires
		t.tokenUntil = time.Now().Add(time.Duration(out.Auth.LeaseDuration) * time.Second * 3 / 4)
	}
	return t.token, nil
}

// do sends an authenticated request; with Kubernetes auth a refused token is
// replaced by a fresh login once.
func (t *Transit) do(ctx context.Context, op, method, path string, body, out any) error {
	token, err := t.authToken(ctx)
	if err != nil {
		return err
	}
	err = t.send(ctx, op, method, path, token, body, out, true)
	if t.cfg.KubernetesRole != "" && errors.Is(err, ErrDenied) {
		t.mu.Lock()
		if t.token == token {
			t.token = ""
		}
		t.mu.Unlock()
		if token, err = t.authToken(ctx); err != nil {
			return err
		}
		err = t.send(ctx, op, method, path, token, body, out, true)
	}
	return err
}

func (t *Transit) send(ctx context.Context, op, method, path, token string, body, out any, data bool) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	t.header(req, token)
	res, err := t.hc.Do(req)
	if err != nil {
		return &TransitError{Op: op, Messages: []string{err.Error()}, kind: ErrUnavailable}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return &TransitError{Op: op, Status: res.StatusCode, Messages: []string{err.Error()}, kind: ErrUnavailable}
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []string        `json:"errors"`
	}
	_ = json.Unmarshal(raw, &env)
	if res.StatusCode >= 300 {
		return transitFailure(op, res.StatusCode, env.Errors)
	}
	if out == nil {
		return nil
	}
	src := raw
	if data {
		src = env.Data
	}
	if len(src) == 0 || string(src) == "null" {
		return &TransitError{Op: op, Status: res.StatusCode, Messages: []string{"the server returned no data"}, kind: ErrUnavailable}
	}
	if err := json.Unmarshal(src, out); err != nil {
		return &TransitError{Op: op, Status: res.StatusCode, Messages: []string{"unreadable response: " + err.Error()}, kind: ErrUnavailable}
	}
	return nil
}

func transitFailure(op string, status int, msgs []string) error {
	// the server's messages may span lines ("1 error occurred:\n\t* permission denied"): one line each
	for i, m := range msgs {
		msgs[i] = strings.Join(strings.Fields(m), " ")
	}
	e := &TransitError{Op: op, Status: status, Messages: msgs, kind: ErrUnavailable}
	joined := strings.ToLower(strings.Join(msgs, "; "))
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.kind = ErrDenied
	case strings.Contains(joined, "not found") || (status == http.StatusNotFound && op != "login"):
		e.kind = ErrUnknownKey
	case status == http.StatusBadRequest && (op == "decrypt" || op == "rewrap") &&
		(strings.Contains(joined, "authentication failed") || strings.Contains(joined, "invalid ciphertext") ||
			strings.Contains(joined, "unable to decrypt") || strings.Contains(joined, "decryption failed") ||
			strings.Contains(joined, "invalid") && strings.Contains(joined, "ciphertext")):
		e.kind = ErrDecrypt
	case status == http.StatusBadRequest:
		e.kind = ErrConfig
	}
	return e
}

// TransitError is a refused or failed transit request. errors.Is matches its
// kind: ErrUnavailable (not reached, sealed, a server error), ErrDenied (the
// login or the token's policy), ErrUnknownKey, ErrDecrypt or ErrConfig.
type TransitError struct {
	Op       string
	Status   int
	Messages []string
	kind     error
}

func (e *TransitError) Error() string {
	s := "kms: transit " + e.Op
	if e.Status != 0 {
		s += ": " + strconv.Itoa(e.Status)
	}
	if len(e.Messages) > 0 {
		s += ": " + strings.Join(e.Messages, "; ")
	}
	return s
}

func (e *TransitError) Unwrap() error { return e.kind }
