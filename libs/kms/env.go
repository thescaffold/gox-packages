package kms

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
)

// FromEnv builds a KMS from the environment:
//
//	KMS_DRIVER        local (default) or transit
//
// local:
//
//	KMS_LOCAL_KEYS    id:base64key[,id:base64key…]  (each key 32 bytes)
//	KMS_LOCAL_CURRENT the id new wraps use (default: the last one listed)
//
// transit (OpenBao or Vault's transit engine):
//
//	KMS_TRANSIT_ADDR             the server, e.g. https://openbao.openbao.svc:8200
//	KMS_TRANSIT_MOUNT            the engine's mount (default transit)
//	KMS_TRANSIT_KEY              the key's name
//	KMS_TRANSIT_NAMESPACE        X-Vault-Namespace (Vault Enterprise, OpenBao namespaces)
//	KMS_TRANSIT_CA_FILE          a PEM bundle the server's certificate chains to
//	KMS_TRANSIT_TOKEN            a token, or
//	KMS_TRANSIT_TOKEN_FILE       a file holding one (read on each request), or
//	KMS_TRANSIT_KUBERNETES_ROLE  Kubernetes auth with this role, logging in with
//	KMS_TRANSIT_KUBERNETES_TOKEN_FILE (default the pod's service account token)
//	at auth/KMS_TRANSIT_KUBERNETES_MOUNT (default kubernetes)
//
// With transit, KMS_LOCAL_KEYS may stay set while an install moves over: new
// wraps use transit and keys the Local driver wrapped still unwrap (Chain).
//
// With no local keys configured and the local driver, it returns nil and no
// error: the caller decides whether running without a KMS is acceptable.
// FromEnv makes no request; transit clients with the same settings are shared
// within the process, so they log in once.
func FromEnv(getenv func(string) string) (KMS, error) {
	local, err := localFromEnv(getenv)
	if err != nil {
		return nil, err
	}
	switch d := strings.ToLower(strings.TrimSpace(getenv("KMS_DRIVER"))); d {
	case "", "local":
		if local == nil {
			return nil, nil // a nil *Local is not a nil KMS
		}
		return local, nil
	case "transit":
		t, err := transitFromEnv(getenv)
		if err != nil {
			return nil, err
		}
		if local != nil {
			return Chain{t, local}, nil
		}
		return t, nil
	default:
		return nil, fmt.Errorf("%w: unknown KMS_DRIVER %q (local or transit)", ErrConfig, d)
	}
}

// Driver names the driver the environment selects: "local" or "transit".
func Driver(getenv func(string) string) string {
	if d := strings.ToLower(strings.TrimSpace(getenv("KMS_DRIVER"))); d != "" {
		return d
	}
	return "local"
}

func localFromEnv(getenv func(string) string) (*Local, error) {
	raw := strings.TrimSpace(getenv("KMS_LOCAL_KEYS"))
	if raw == "" {
		return nil, nil
	}
	keys := map[string][]byte{}
	last := ""
	for _, part := range strings.Split(raw, ",") {
		id, b64, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("%w: KMS_LOCAL_KEYS entries are id:base64key", ErrConfig)
		}
		k, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("%w: key %q is not base64", ErrConfig, id)
		}
		keys[id] = k
		last = id
	}
	current := strings.TrimSpace(getenv("KMS_LOCAL_CURRENT"))
	if current == "" {
		current = last
	}
	return NewLocal(keys, current)
}

var (
	transitMu     sync.Mutex
	transitShared = map[TransitConfig]*Transit{}
)

func transitFromEnv(getenv func(string) string) (*Transit, error) {
	g := func(k string) string { return strings.TrimSpace(getenv(k)) }
	cfg := TransitConfig{
		Address:             g("KMS_TRANSIT_ADDR"),
		Mount:               g("KMS_TRANSIT_MOUNT"),
		Key:                 g("KMS_TRANSIT_KEY"),
		Namespace:           g("KMS_TRANSIT_NAMESPACE"),
		CAFile:              g("KMS_TRANSIT_CA_FILE"),
		Token:               g("KMS_TRANSIT_TOKEN"),
		TokenFile:           g("KMS_TRANSIT_TOKEN_FILE"),
		KubernetesRole:      g("KMS_TRANSIT_KUBERNETES_ROLE"),
		KubernetesMount:     g("KMS_TRANSIT_KUBERNETES_MOUNT"),
		KubernetesTokenFile: g("KMS_TRANSIT_KUBERNETES_TOKEN_FILE"),
	}
	if cfg.Address == "" {
		return nil, fmt.Errorf("%w: KMS_DRIVER=transit needs KMS_TRANSIT_ADDR", ErrConfig)
	}
	if cfg.Key == "" {
		return nil, fmt.Errorf("%w: KMS_DRIVER=transit needs KMS_TRANSIT_KEY", ErrConfig)
	}
	transitMu.Lock()
	defer transitMu.Unlock()
	if t, ok := transitShared[cfg]; ok {
		return t, nil
	}
	t, err := NewTransit(cfg)
	if err != nil {
		return nil, err
	}
	transitShared[cfg] = t
	return t, nil
}
