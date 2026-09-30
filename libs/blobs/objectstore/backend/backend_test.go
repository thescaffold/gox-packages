package backend_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/backend"
)

var key64 = strings.Repeat("ab", 32) // 64 hex chars = 32 bytes

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnv_DefaultsToPostgres(t *testing.T) {
	c, err := backend.FromEnv(env(map[string]string{"BLOBS_MASTER_KEY": key64, "BLOBS_PRESIGN_SECRET": "0123456789abcdef0123"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend != "postgres" || len(c.MasterKey) != 32 {
		t.Fatalf("%+v", c)
	}
}

func TestFromEnv_ParsesEverything(t *testing.T) {
	c, err := backend.FromEnv(env(map[string]string{
		"BLOBS_BACKEND": "FS", "BLOBS_FS_ROOT": "/var/blobs", "BLOBS_MAX_OBJECT_SIZE": "5242880",
		"BLOBS_MASTER_KEY": key64, "BLOBS_PRESIGN_SECRET": "0123456789abcdef0123", "BLOBS_MAX_CONCURRENT_READS": "4",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend != "fs" || c.FSRoot != "/var/blobs" || c.MaxObjectSize != 5<<20 || c.MaxConcurrentReads != 4 {
		t.Fatalf("%+v", c)
	}
}

func TestFromEnv_RejectsBadValues(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"unknown backend":  {"BLOBS_BACKEND": "floppy"},
		"short master key": {"BLOBS_MASTER_KEY": "abcd"},
		"non-hex key":      {"BLOBS_MASTER_KEY": strings.Repeat("zz", 32)},
		"bad size":         {"BLOBS_MAX_OBJECT_SIZE": "lots"},
		"negative size":    {"BLOBS_MAX_OBJECT_SIZE": "-1"},
		"short secret":     {"BLOBS_PRESIGN_SECRET": "short"},
	} {
		if _, err := backend.FromEnv(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOpen_FS(t *testing.T) {
	st, err := backend.Open(context.Background(), backend.Config{Backend: "fs", FSRoot: t.TempDir(), PresignSecret: []byte("0123456789abcdef0123")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(t.Context(), "ws/a/k", strings.NewReader("x"), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestOpen_FailsClearlyWhenMisconfigured(t *testing.T) {
	ctx := context.Background()
	if _, err := backend.Open(ctx, backend.Config{Backend: "s3"}); err == nil || !strings.Contains(err.Error(), "M7") {
		t.Fatalf("s3: %v (must say it is not built yet, not silently fall back)", err)
	}
	if _, err := backend.Open(ctx, backend.Config{Backend: "postgres"}); err == nil {
		t.Fatal("postgres with no database / key was accepted")
	}
	if _, err := backend.Open(ctx, backend.Config{Backend: "postgres", DB: &sql.DB{}}); err == nil {
		t.Fatal("postgres with no master key was accepted")
	}
	if _, err := backend.Open(ctx, backend.Config{Backend: "fs"}); err == nil {
		t.Fatal("fs with no root was accepted")
	}
	if _, err := backend.Open(ctx, backend.Config{Backend: ""}); err == nil {
		t.Fatal("an empty backend must be resolved by FromEnv, not guessed by Open")
	}
}
