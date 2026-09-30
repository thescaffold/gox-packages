// Package backend selects the ObjectStore driver from configuration
// (BLOBS_BACKEND=postgres|fs|s3, default postgres; TRD §6.10). No caller names
// a backend: they receive an objectstore.ObjectStore.
package backend

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/fs"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/pg"
)

// Config is the resolved configuration.
type Config struct {
	Backend            string  // postgres (default) | fs | s3
	FSRoot             string  // fs only
	DB                 *sql.DB // postgres only: the DEDICATED blob pool
	Schema             string
	MasterKey          []byte
	PresignSecret      []byte
	MaxObjectSize      int64
	MaxConcurrentReads int
	Quota              func(ctx context.Context, workspaceID string) (int64, bool)
}

// FromEnv reads BLOBS_* variables through get (os.Getenv in production). Key
// material is 64 hex characters (BLOBS_MASTER_KEY, 32 bytes) and at least 16
// characters (BLOBS_PRESIGN_SECRET). Unset values are left zero so Open can
// reject what its backend requires; nothing is silently defaulted to an
// insecure value.
func FromEnv(get func(string) string) (Config, error) {
	c := Config{Backend: strings.ToLower(strings.TrimSpace(get("BLOBS_BACKEND"))), FSRoot: get("BLOBS_FS_ROOT"), Schema: get("BLOBS_SCHEMA")}
	if c.Backend == "" {
		c.Backend = "postgres"
	}
	switch c.Backend {
	case "postgres", "fs", "s3":
	default:
		return Config{}, fmt.Errorf("backend: BLOBS_BACKEND=%q is not one of postgres, fs, s3", c.Backend)
	}
	if v := get("BLOBS_MASTER_KEY"); v != "" {
		k, err := hex.DecodeString(v)
		if err != nil || len(k) != 32 {
			return Config{}, errors.New("backend: BLOBS_MASTER_KEY must be 64 hex characters (32 bytes)")
		}
		c.MasterKey = k
	}
	if v := get("BLOBS_PRESIGN_SECRET"); v != "" {
		if len(v) < 16 {
			return Config{}, errors.New("backend: BLOBS_PRESIGN_SECRET must be at least 16 characters")
		}
		c.PresignSecret = []byte(v)
	}
	for _, p := range []struct {
		env string
		dst *int64
	}{{"BLOBS_MAX_OBJECT_SIZE", &c.MaxObjectSize}} {
		if v := get(p.env); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return Config{}, fmt.Errorf("backend: %s must be a positive integer", p.env)
			}
			*p.dst = n
		}
	}
	if v := get("BLOBS_MAX_CONCURRENT_READS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, errors.New("backend: BLOBS_MAX_CONCURRENT_READS must be a positive integer")
		}
		c.MaxConcurrentReads = n
	}
	return c, nil
}

// Open builds the configured store. The Postgres backend also runs its
// (idempotent) migration.
func Open(ctx context.Context, c Config) (objectstore.ObjectStore, error) {
	switch c.Backend {
	case "postgres":
		if c.DB == nil {
			return nil, errors.New("backend: postgres needs a database pool")
		}
		if len(c.MasterKey) != 32 {
			return nil, errors.New("backend: postgres needs BLOBS_MASTER_KEY (64 hex characters)")
		}
		if err := pg.Migrate(ctx, c.DB, c.Schema); err != nil {
			return nil, err
		}
		return pg.New(pg.Config{DB: c.DB, Schema: c.Schema, MasterKey: c.MasterKey, PresignSecret: c.PresignSecret,
			MaxObjectSize: c.MaxObjectSize, MaxConcurrentReads: c.MaxConcurrentReads, Quota: c.Quota})
	case "fs":
		if c.FSRoot == "" {
			return nil, errors.New("backend: fs needs BLOBS_FS_ROOT")
		}
		return fs.New(c.FSRoot, fs.Config{MaxObjectSize: c.MaxObjectSize, PresignSecret: c.PresignSecret})
	case "s3":
		return nil, errors.New("backend: the s3 driver is not built yet (PLAN M7-15a); use BLOBS_BACKEND=postgres")
	}
	return nil, fmt.Errorf("backend: unknown backend %q (resolve it with FromEnv)", c.Backend)
}
