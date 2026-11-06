# kms

Key-management port for envelope encryption: a data key encrypts values, and a key-encryption key held by a KMS wraps the data key. Only wrapped data keys are stored.

```go
k, _ := kms.FromEnv(os.Getenv)             // KMS_DRIVER=local, KMS_LOCAL_KEYS=id:base64,…
dek, _ := kms.NewDataKey()
keyID, wrapped, _ := k.Wrap(ctx, dek, []byte(workspaceID))   // store keyID + wrapped
sealed, _ := kms.Seal(dek, []byte(value), []byte(itemID))     // AES-256-GCM, aad-bound
```

Drivers: `Local` (configured keys, rotation by listing several and naming the current one). Cloud KMS and OpenBao transit drivers implement the same `KMS` interface. No dependencies beyond the standard library.
