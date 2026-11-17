# kms

Key-management port for envelope encryption: a data key encrypts values, and a key-encryption key held by a KMS wraps the data key. Only wrapped data keys are stored.

```go
k, _ := kms.FromEnv(os.Getenv)             // KMS_DRIVER=local|transit, and the driver's settings
dek, _ := kms.NewDataKey()
keyID, wrapped, _ := k.Wrap(ctx, dek, []byte(workspaceID))   // store keyID + wrapped
sealed, _ := kms.Seal(dek, []byte(value), []byte(itemID))     // AES-256-GCM, aad-bound
```

No dependencies beyond the standard library.

## Drivers

- **`Local`**: configured keys (`KMS_LOCAL_KEYS=id:base64,…`); rotate by listing several and naming the current one (`KMS_LOCAL_CURRENT`).
- **`Transit`**: the transit secrets engine of OpenBao or HashiCorp Vault (the same HTTP API). The key never leaves the server: `Wrap` is `encrypt`, `Unwrap` is `decrypt`. Cloud KMS drivers implement the same `KMS` interface.

### Transit

```sh
KMS_DRIVER=transit
KMS_TRANSIT_ADDR=https://openbao.openbao.svc:8200
KMS_TRANSIT_MOUNT=transit                   # default
KMS_TRANSIT_KEY=origine
KMS_TRANSIT_CA_FILE=/etc/kms/ca.crt         # optional: the server's CA
KMS_TRANSIT_NAMESPACE=team-a                # optional: X-Vault-Namespace
# auth, one of:
KMS_TRANSIT_TOKEN=…                         # or KMS_TRANSIT_TOKEN_FILE (read on each request)
KMS_TRANSIT_KUBERNETES_ROLE=origine         # Kubernetes auth with the pod's service account token
KMS_TRANSIT_KUBERNETES_MOUNT=kubernetes     # default
KMS_TRANSIT_KUBERNETES_TOKEN_FILE=…         # default the pod's mounted token
```

- **The key must be derived** (`bao write -f transit/keys/origine derived=true`). `aad` is bound through key derivation: every request sends `"kms\x00" + aad` as the transit `context`, so a wrapped key decrypts only under the context it was wrapped with, as `Local`'s AES-GCM aad does. Transit's `associated_data` would bind it too, but `rewrap` does not accept it (OpenBao 2.4), so rotated keys could not be rewrapped inside the server. Transit silently ignores the context of a key that is not derived, so `Wrap` reads the key once per process and refuses one that is not.
- **Policy**: `update` on `<mount>/encrypt/<key>`, `<mount>/decrypt/<key>` and `<mount>/rewrap/<key>`; `read` on `<mount>/keys/<key>`.
- **Key ids** are `transit:<mount>/<key>`; the key version is inside the wrapped value (`vault:v3:…`, `WrappedVersion`). `Unwrap` uses the key the id names, so data wrapped under an older key name still unwraps.
- **Kubernetes auth** logs in once per process (`FromEnv` shares clients with the same settings), again before the token's lease ends, and once more when a request is refused.
- `Health` (sys/health), `Login`, `Key` and `Probe` give the steps of an install check, each with its own error (`ErrUnavailable`, `ErrDenied`, `ErrUnknownKey`, `ErrConfig`).

## Rotation

- Local: add a key, make it current; the old one keeps unwrapping.
- Transit: `bao write -f transit/keys/origine/rotate`. New wraps use the newest version and older versions still unwrap.
- `kms.Rewrap(ctx, k, keyID, wrapped, aad)` moves a stored key to the current key encryption key, keeping its aad. With transit it calls `rewrap`, so the data key never leaves the server; other drivers unwrap and wrap again in memory. Once every stored key is rewrapped, the old version can be retired (`min_decryption_version`) or the old local key removed.
- `Chain{new, old}` wraps with `new` and unwraps with whichever knows the key id. `FromEnv` returns `Chain{transit, local}` when `KMS_DRIVER=transit` and `KMS_LOCAL_KEYS` is still set, so an install can move from Local to transit and rewrap at its own pace.

## Tests

```sh
go test ./...
# against a real OpenBao (or Vault) in dev mode:
docker run -d -p 127.0.0.1:8200:8200 -e BAO_DEV_ROOT_TOKEN_ID=root openbao/openbao server -dev -dev-listen-address=0.0.0.0:8200
KMS_TEST_TRANSIT_ADDR=http://127.0.0.1:8200 KMS_TEST_TRANSIT_TOKEN=root go test -run Integration ./...
```
