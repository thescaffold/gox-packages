package pg

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Migrate creates (idempotently) the blob schema: BlobObjects, BlobChunks,
// BlobParts, BlobUsage, the immutability triggers for locked objects and the
// indexes the driver's queries use. Everything lives in its own schema
// (default "blobs") so blob I/O, WAL volume and vacuum are easy to separate
// from the control plane's tables (TRD §6.10).
func Migrate(ctx context.Context, db *sql.DB, schema string) error {
	if schema == "" {
		schema = "blobs"
	}
	if !schemaName.MatchString(schema) {
		return fmt.Errorf("pg: invalid schema name %q", schema)
	}
	q := func(format string) string { return fmt.Sprintf(format, schema) }
	stmts := []string{
		q(`CREATE SCHEMA IF NOT EXISTS %[1]s`),

		q(`CREATE TABLE IF NOT EXISTS %[1]s."BlobObjects" (
			id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id    text        NOT NULL DEFAULT '',
			key             text        NOT NULL,
			version         bigint      NOT NULL DEFAULT 1,
			size            bigint      NOT NULL DEFAULT 0,
			sha256          text,
			media_type      text,
			encryption      text        NOT NULL DEFAULT 'aes-256-gcm-v1',
			wrapped_key     text        NOT NULL,
			status          text        NOT NULL DEFAULT 'writing' CHECK (status IN ('writing','committed','deleted')),
			retention_class text        NOT NULL DEFAULT 'permanent' CHECK (retention_class IN ('permanent','ephemeral')),
			locked          boolean     NOT NULL DEFAULT false,
			metadata        jsonb,
			chunk_size      integer     NOT NULL,
			layout          jsonb       NOT NULL DEFAULT '[]'::jsonb,
			upload_id       text,
			put_opts        jsonb,
			created_at      timestamptz NOT NULL DEFAULT now(),
			committed_at    timestamptz,
			expires_at      timestamptz,
			deleted_at      timestamptz
		)`),
		// One committed object per key; 'writing' rows of the same key may coexist.
		q(`CREATE UNIQUE INDEX IF NOT EXISTS "uq_blob_objects_key_committed" ON %[1]s."BlobObjects" (key) WHERE status = 'committed'`),
		q(`CREATE INDEX IF NOT EXISTS "idx_blob_objects_key_prefix" ON %[1]s."BlobObjects" (key text_pattern_ops) WHERE status = 'committed'`),
		q(`CREATE INDEX IF NOT EXISTS "idx_blob_objects_workspace" ON %[1]s."BlobObjects" (workspace_id)`),
		q(`CREATE INDEX IF NOT EXISTS "idx_blob_objects_status_created" ON %[1]s."BlobObjects" (status, created_at)`),
		q(`CREATE UNIQUE INDEX IF NOT EXISTS "uq_blob_objects_upload" ON %[1]s."BlobObjects" (upload_id) WHERE upload_id IS NOT NULL`),
		q(`CREATE INDEX IF NOT EXISTS "idx_blob_objects_expires" ON %[1]s."BlobObjects" (expires_at) WHERE expires_at IS NOT NULL AND status = 'committed'`),

		q(`CREATE TABLE IF NOT EXISTS %[1]s."BlobChunks" (
			object_id uuid   NOT NULL REFERENCES %[1]s."BlobObjects"(id) ON DELETE CASCADE,
			seq       bigint NOT NULL,
			data      bytea  NOT NULL,
			PRIMARY KEY (object_id, seq)
		)`),
		// Chunks are already ciphertext: skip TOAST compression, which only costs CPU.
		q(`ALTER TABLE %[1]s."BlobChunks" ALTER COLUMN data SET STORAGE EXTERNAL`),

		q(`CREATE TABLE IF NOT EXISTS %[1]s."BlobParts" (
			object_id uuid    NOT NULL REFERENCES %[1]s."BlobObjects"(id) ON DELETE CASCADE,
			part      integer NOT NULL,
			size      bigint  NOT NULL,
			sha256    text    NOT NULL,
			PRIMARY KEY (object_id, part)
		)`),

		q(`CREATE TABLE IF NOT EXISTS %[1]s."BlobUsage" (
			workspace_id text   PRIMARY KEY,
			bytes        bigint NOT NULL DEFAULT 0
		)`),

		// Single-use download tokens already spent (shared across instances).
		q(`CREATE TABLE IF NOT EXISTS %[1]s."BlobSpentTokens" (
			id         text        PRIMARY KEY,
			expires_at timestamptz NOT NULL
		)`),
		q(`CREATE INDEX IF NOT EXISTS "idx_blob_spent_expires" ON %[1]s."BlobSpentTokens" (expires_at)`),

		// ── immutability for locked objects ───────────────────────────────
		// Tamper RESISTANCE against application bugs and ordinary
		// credentials; tamper EVIDENCE comes from the SHA-256 in the signed
		// Evidence record (TRD §6.10). Members of role blobs_retention (if the
		// operator creates it) are exempt, for retention purges.
		q(`CREATE OR REPLACE FUNCTION %[1]s.blob_is_retention() RETURNS boolean LANGUAGE sql STABLE AS $f$
			SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'blobs_retention')
			   AND pg_has_role(current_user, 'blobs_retention', 'MEMBER')
		$f$`),
		q(`CREATE OR REPLACE FUNCTION %[1]s.blob_guard_objects() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN
			IF OLD.locked AND OLD.status = 'committed' AND NOT %[1]s.blob_is_retention() THEN
				RAISE EXCEPTION 'blobs: locked object % is immutable', OLD.id USING ERRCODE = '42501';
			END IF;
			IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
			RETURN NEW;
		END $f$`),
		q(`DROP TRIGGER IF EXISTS blob_guard_objects ON %[1]s."BlobObjects"`),
		q(`CREATE TRIGGER blob_guard_objects BEFORE UPDATE OR DELETE ON %[1]s."BlobObjects"
			FOR EACH ROW EXECUTE FUNCTION %[1]s.blob_guard_objects()`),
		q(`CREATE OR REPLACE FUNCTION %[1]s.blob_guard_chunks() RETURNS trigger LANGUAGE plpgsql AS $f$
		DECLARE oid uuid; is_locked boolean;
		BEGIN
			IF TG_OP = 'INSERT' THEN oid := NEW.object_id; ELSE oid := OLD.object_id; END IF;
			SELECT locked AND status = 'committed' INTO is_locked FROM %[1]s."BlobObjects" WHERE id = oid;
			IF COALESCE(is_locked, false) AND NOT %[1]s.blob_is_retention() THEN
				RAISE EXCEPTION 'blobs: chunks of locked object % are immutable', oid USING ERRCODE = '42501';
			END IF;
			IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
			RETURN NEW;
		END $f$`),
		q(`DROP TRIGGER IF EXISTS blob_guard_chunks ON %[1]s."BlobChunks"`),
		q(`CREATE TRIGGER blob_guard_chunks BEFORE INSERT OR UPDATE OR DELETE ON %[1]s."BlobChunks"
			FOR EACH ROW EXECUTE FUNCTION %[1]s.blob_guard_chunks()`),
	}
	// Serialise concurrent migrators (two replicas booting together).
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('origine_blobs_migrate'))`); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtext('origine_blobs_migrate'))`)
	}()
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("pg: migrate: %w\n%s", err, s)
		}
	}
	return nil
}
