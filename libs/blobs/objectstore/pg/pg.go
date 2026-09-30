// Package pg is the Postgres ObjectStore driver — the production default
// (TRD §6.10, PLAN M1-05a). Content lives in chunked, AES-GCM-sealed bytea
// rows (BlobChunks), never as one value and never held whole in memory.
//
// Write: insert the object as `writing`, append chunks in small autocommit
// statements while hashing, then flip to `committed` with size and SHA-256 in
// one short transaction — atomic visibility, no long transaction. A killed
// writer leaves only a `writing` row that Sweep removes.
//
// Read: chunks are fetched in small keyset batches (one short query each), so
// a slow client never holds a database connection between batches; ranges are
// chunk arithmetic.
package pg

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
)

// Config configures the driver.
type Config struct {
	// DB should be a DEDICATED, bounded pool (db.SetMaxOpenConns) so blob I/O
	// cannot starve the control plane's connections.
	DB     *sql.DB
	Schema string // default "blobs"
	// MasterKey is the 32-byte root of the envelope-encryption hierarchy.
	MasterKey []byte
	// MaxObjectSize defaults to objectstore.DefaultMaxObjectSize (100 MiB).
	MaxObjectSize int64
	// ChunkSize is the plaintext bytes per chunk; default 2 MiB.
	ChunkSize int
	// PresignSecret signs download URLs (>= 16 bytes).
	PresignSecret []byte
	// MaxConcurrentReads caps simultaneously open Get readers per instance
	// (default 16); further Gets wait for a slot (or their context).
	MaxConcurrentReads int
	// Quota returns the storage allowance in bytes for a workspace; ok=false
	// means unlimited. Enforced at commit.
	Quota func(ctx context.Context, workspaceID string) (bytes int64, ok bool)
	// WritingTTL is how long an unfinished object/upload survives before Sweep
	// removes it (default 1h). DeletedGrace is how long a deleted/replaced
	// object's chunks are kept so in-flight readers can finish (default 10m).
	WritingTTL   time.Duration
	DeletedGrace time.Duration
	Now          func() time.Time
}

// Store implements objectstore.ObjectStore.
type Store struct {
	cfg  Config
	sch  string
	sem  chan struct{}
	once sync.Once
}

var _ objectstore.ObjectStore = (*Store)(nil)

// New validates cfg and returns a store. Call Migrate first.
func New(cfg Config) (*Store, error) {
	if cfg.DB == nil {
		return nil, errors.New("pg: DB is required")
	}
	if len(cfg.MasterKey) != 32 {
		return nil, errors.New("pg: MasterKey must be exactly 32 bytes")
	}
	if cfg.Schema == "" {
		cfg.Schema = "blobs"
	}
	if !schemaName.MatchString(cfg.Schema) {
		return nil, fmt.Errorf("pg: invalid schema name %q", cfg.Schema)
	}
	if cfg.MaxObjectSize <= 0 {
		cfg.MaxObjectSize = objectstore.DefaultMaxObjectSize
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 2 << 20
	}
	if cfg.MaxConcurrentReads <= 0 {
		cfg.MaxConcurrentReads = 16
	}
	if cfg.WritingTTL <= 0 {
		cfg.WritingTTL = time.Hour
	}
	if cfg.DeletedGrace <= 0 {
		cfg.DeletedGrace = 10 * time.Minute
	}
	return &Store{cfg: cfg, sch: cfg.Schema, sem: make(chan struct{}, cfg.MaxConcurrentReads)}, nil
}

func (s *Store) t(name string) string { return s.sch + `."` + name + `"` }

func (s *Store) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

// workspaceOf derives the tenant from the key layout ws/<id>/...; keys outside
// it belong to no workspace ("" — no quota, shared).
func workspaceOf(key string) string {
	rest, ok := strings.CutPrefix(key, "ws/")
	if !ok {
		return ""
	}
	id, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return id
}

type segment struct {
	FirstSeq int64 `json:"firstSeq"`
	Size     int64 `json:"size"`
}

type objRow struct {
	id, workspace, key, wrapped string
	size                        int64
	sha                         string
	media                       sql.NullString
	locked                      bool
	metadata                    map[string]string
	chunkSize                   int
	layout                      []segment
	created                     time.Time
}

func (o objRow) info() objectstore.ObjectInfo {
	return objectstore.ObjectInfo{Key: o.key, Size: o.size, SHA256: o.sha, MediaType: o.media.String, CreatedAt: o.created, Metadata: o.metadata, Locked: o.locked}
}

const objCols = `id::text, workspace_id, key, wrapped_key, size, COALESCE(sha256,''), media_type, locked, metadata, chunk_size, layout, created_at`

type scanner interface{ Scan(...any) error }

func scanObj(r scanner) (objRow, error) {
	var o objRow
	var md, lay []byte
	if err := r.Scan(&o.id, &o.workspace, &o.key, &o.wrapped, &o.size, &o.sha, &o.media, &o.locked, &md, &o.chunkSize, &lay, &o.created); err != nil {
		return objRow{}, err
	}
	if len(md) > 0 {
		_ = json.Unmarshal(md, &o.metadata)
	}
	_ = json.Unmarshal(lay, &o.layout)
	return o, nil
}

func (s *Store) loadCommitted(ctx context.Context, key string) (objRow, error) {
	o, err := scanObj(s.cfg.DB.QueryRowContext(ctx,
		`SELECT `+objCols+` FROM `+s.t("BlobObjects")+` WHERE key = $1 AND status = 'committed'`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return objRow{}, fmt.Errorf("%w: %s", objectstore.ErrNotFound, key)
	}
	return o, err
}

func lowerKeys(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

// ── write path ───────────────────────────────────────────────────────────────

// begin inserts a `writing` object row and returns its id and data key.
func (s *Store) begin(ctx context.Context, key string, opts objectstore.PutOptions, uploadID string) (id string, dataKey []byte, err error) {
	ws := workspaceOf(key)
	dataKey = make([]byte, 32)
	if _, err = rand.Read(dataKey); err != nil {
		return "", nil, err
	}
	// The wrapped key is bound to the object id, which the database generates;
	// generate it here so it can be used for wrapping and the insert.
	idb := make([]byte, 16)
	if _, err = rand.Read(idb); err != nil {
		return "", nil, err
	}
	idb[6] = (idb[6] & 0x0f) | 0x40
	idb[8] = (idb[8] & 0x3f) | 0x80
	id = fmt.Sprintf("%x-%x-%x-%x-%x", idb[0:4], idb[4:6], idb[6:8], idb[8:10], idb[10:16])
	wrapped, err := wrapKey(s.cfg.MasterKey, ws, id, dataKey)
	if err != nil {
		return "", nil, err
	}
	md, _ := json.Marshal(lowerKeys(opts.Metadata))
	po, _ := json.Marshal(struct {
		IfNotExists bool   `json:"ifNotExists,omitempty"`
		Expected    string `json:"expected,omitempty"`
	}{opts.IfNotExists, opts.ExpectedSHA256})
	var exp any
	if !opts.ExpiresAt.IsZero() {
		exp = opts.ExpiresAt.UTC()
	}
	var up any
	if uploadID != "" {
		up = uploadID
	}
	retention := "permanent"
	if !opts.ExpiresAt.IsZero() {
		retention = "ephemeral"
	}
	_, err = s.cfg.DB.ExecContext(ctx, `INSERT INTO `+s.t("BlobObjects")+`
		(id, workspace_id, key, wrapped_key, status, retention_class, locked, metadata, chunk_size, upload_id, put_opts, media_type, expires_at)
		VALUES ($1,$2,$3,$4,'writing',$5,$6,$7::jsonb,$8,$9,$10::jsonb,$11,$12)`,
		id, ws, key, wrapped, retention, opts.Locked, string(md), s.cfg.ChunkSize, up, string(po), nullStr(opts.MediaType), exp)
	return id, dataKey, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// abandon removes a writing object (cascading its chunks), ignoring the
// caller's cancelled context.
func (s *Store) abandon(id string) {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.cfg.DB.ExecContext(c, `DELETE FROM `+s.t("BlobObjects")+` WHERE id = $1 AND status = 'writing'`, id)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// writeChunks streams r into chunks starting at firstSeq, returning the byte
// count and SHA-256. limit bounds the bytes accepted (ErrTooLarge beyond it).
func (s *Store) writeChunks(ctx context.Context, id string, dataKey []byte, r io.Reader, firstSeq, limit int64) (size int64, sum [32]byte, chunks int64, err error) {
	aead, err := newAEAD(dataKey)
	if err != nil {
		return 0, sum, 0, err
	}
	h := sha256.New()
	buf := make([]byte, s.cfg.ChunkSize)
	lr := ctxReader{ctx, io.LimitReader(r, limit+1)}
	for {
		n, rerr := io.ReadFull(lr, buf)
		if n > 0 {
			size += int64(n)
			if size > limit {
				return 0, sum, 0, objectstore.ErrTooLarge
			}
			h.Write(buf[:n])
			blob, err := seal(aead, buf[:n], chunkAAD(id, firstSeq+chunks))
			if err != nil {
				return 0, sum, 0, err
			}
			if _, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO `+s.t("BlobChunks")+` (object_id, seq, data) VALUES ($1,$2,$3)`, id, firstSeq+chunks, blob); err != nil {
				return 0, sum, 0, err
			}
			chunks++
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return 0, sum, 0, rerr
		}
	}
	copy(sum[:], h.Sum(nil))
	return size, sum, chunks, nil
}

func isPgCode(err error, code string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == code
}

// commit publishes object id as key in one short transaction: take the key's
// advisory lock, refuse to replace a locked object, honour IfNotExists, apply
// the quota, retire the previous committed object and flip this one.
func (s *Store) commit(ctx context.Context, id, key string, size int64, sum string, layout []segment, opts objectstore.PutOptions) (objectstore.ObjectInfo, error) {
	if opts.ExpectedSHA256 != "" && !strings.EqualFold(opts.ExpectedSHA256, sum) {
		return objectstore.ObjectInfo{}, objectstore.ErrChecksumMismatch
	}
	ws := workspaceOf(key)
	lay, _ := json.Marshal(layout)
	tx, err := s.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	var oldID sql.NullString
	var oldSize int64
	var oldLocked bool
	err = tx.QueryRowContext(ctx, `SELECT id::text, size, locked FROM `+s.t("BlobObjects")+` WHERE key = $1 AND status = 'committed'`, key).Scan(&oldID, &oldSize, &oldLocked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return objectstore.ObjectInfo{}, err
	}
	if oldID.Valid && oldLocked {
		return objectstore.ObjectInfo{}, fmt.Errorf("%w: %s", objectstore.ErrLocked, key)
	}
	if oldID.Valid && opts.IfNotExists {
		return objectstore.ObjectInfo{}, fmt.Errorf("%w: %s", objectstore.ErrExists, key)
	}
	if ws != "" && s.cfg.Quota != nil {
		if allow, ok := s.cfg.Quota(ctx, ws); ok {
			var used int64
			_ = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT bytes FROM `+s.t("BlobUsage")+` WHERE workspace_id = $1), 0)`, ws).Scan(&used)
			if used-oldSize+size > allow {
				return objectstore.ObjectInfo{}, objectstore.ErrQuotaExceeded
			}
		}
	}
	if oldID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE `+s.t("BlobObjects")+` SET status = 'deleted', deleted_at = now() WHERE id = $1`, oldID.String); err != nil {
			return objectstore.ObjectInfo{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+s.t("BlobObjects")+`
		SET status = 'committed', size = $2, sha256 = $3, layout = $4::jsonb, committed_at = now(), upload_id = NULL
		WHERE id = $1 AND status = 'writing'`, id, size, sum, string(lay)); err != nil {
		if isPgCode(err, "23505") {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: %s", objectstore.ErrExists, key)
		}
		return objectstore.ObjectInfo{}, err
	}
	if ws != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+s.t("BlobUsage")+` (workspace_id, bytes) VALUES ($1, $2)
			ON CONFLICT (workspace_id) DO UPDATE SET bytes = `+s.t("BlobUsage")+`.bytes + $2`, ws, size-oldSize); err != nil {
			return objectstore.ObjectInfo{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	o, err := s.loadCommitted(ctx, key)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return o.info(), nil
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts objectstore.PutOptions) (objectstore.ObjectInfo, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	// Fail fast on a locked/existing key before streaming anything.
	if cur, err := s.loadCommitted(ctx, key); err == nil {
		if cur.locked {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: %s", objectstore.ErrLocked, key)
		}
		if opts.IfNotExists {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: %s", objectstore.ErrExists, key)
		}
	}
	id, dataKey, err := s.begin(ctx, key, opts, "")
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	size, sum, _, err := s.writeChunks(ctx, id, dataKey, r, 0, s.cfg.MaxObjectSize)
	if err != nil {
		s.abandon(id)
		return objectstore.ObjectInfo{}, err
	}
	info, err := s.commit(ctx, id, key, size, hex.EncodeToString(sum[:]), []segment{{FirstSeq: 0, Size: size}}, opts)
	if err != nil {
		s.abandon(id)
		return objectstore.ObjectInfo{}, err
	}
	return info, nil
}

// ── read path ────────────────────────────────────────────────────────────────

func (s *Store) acquire(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) Get(ctx context.Context, key string, rng *objectstore.Range) (io.ReadCloser, objectstore.ObjectInfo, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	o, err := s.loadCommitted(ctx, key)
	if err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	start, length, err := objectstore.ResolveRange(rng, o.size)
	if err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	dataKey, err := unwrapKey(s.cfg.MasterKey, o.workspace, o.id, o.wrapped)
	if err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	aead, err := newAEAD(dataKey)
	if err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	rd := &reader{s: s, ctx: ctx, o: o, aead: aead, off: start, remain: length}
	if length == 0 {
		rd.release()
	}
	return rd, o.info(), nil
}

// reader streams an object's plaintext range chunk-batch by chunk-batch.
type reader struct {
	s      *Store
	ctx    context.Context
	o      objRow
	aead   cipher.AEAD
	off    int64 // next plaintext offset to serve
	remain int64
	buf    []byte // decrypted bytes ready to serve
	done   bool
	err    error
}

func (r *reader) release() {
	if !r.done {
		r.done = true
		<-r.s.sem
	}
}

func (r *reader) Close() error { r.release(); return nil }

// seqFor maps a plaintext offset to (segment, chunk seq, offset within chunk).
func (r *reader) seqFor(off int64) (seq int64, within int64, ok bool) {
	var base int64
	cs := int64(r.o.chunkSize)
	for _, seg := range r.o.layout {
		if off < base+seg.Size {
			idx := (off - base) / cs
			return seg.FirstSeq + idx, (off - base) - idx*cs, true
		}
		base += seg.Size
	}
	return 0, 0, false
}

// segmentEnd returns the last chunk seq of the segment containing seq.
func (r *reader) segmentLast(seq int64) int64 {
	cs := int64(r.o.chunkSize)
	for _, seg := range r.o.layout {
		n := (seg.Size + cs - 1) / cs
		if seq >= seg.FirstSeq && seq < seg.FirstSeq+n {
			return seg.FirstSeq + n - 1
		}
	}
	return seq
}

const readBatch = 4

func (r *reader) fill() error {
	seq, within, ok := r.seqFor(r.off)
	if !ok {
		return io.ErrUnexpectedEOF
	}
	last := r.segmentLast(seq)
	if hi := seq + readBatch - 1; hi < last {
		last = hi
	}
	rows, err := r.s.cfg.DB.QueryContext(r.ctx, `SELECT seq, data FROM `+r.s.t("BlobChunks")+`
		WHERE object_id = $1 AND seq >= $2 AND seq <= $3 ORDER BY seq`, r.o.id, seq, last)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	want := seq
	for rows.Next() {
		var got int64
		var blob []byte
		if err := rows.Scan(&got, &blob); err != nil {
			return err
		}
		if got != want {
			return errTampered // a missing chunk
		}
		plain, err := open(r.aead, blob, chunkAAD(r.o.id, got))
		if err != nil {
			return err
		}
		if within > 0 {
			if within > int64(len(plain)) {
				return errTampered
			}
			plain = plain[within:]
			within = 0
		}
		r.buf = append(r.buf, plain...)
		want++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if want != last+1 {
		return errTampered
	}
	return nil
}

func (r *reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.remain == 0 {
		r.release()
		return 0, io.EOF
	}
	if len(r.buf) == 0 {
		if err := r.ctx.Err(); err != nil {
			r.err = err
			r.release()
			return 0, err
		}
		if err := r.fill(); err != nil {
			r.err = err
			r.release()
			return 0, err
		}
	}
	n := len(r.buf)
	if int64(n) > r.remain {
		n = int(r.remain)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.buf[:n])
	r.buf = r.buf[n:]
	r.off += int64(n)
	r.remain -= int64(n)
	if r.remain == 0 {
		r.release()
	}
	return n, nil
}

// Verify re-reads the whole object and checks its SHA-256 against the stored
// one (exports, evidence, `verify`).
func (s *Store) Verify(ctx context.Context, key string) error {
	rc, info, err := s.Get(ctx, key, nil)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != info.SHA256 {
		return objectstore.ErrChecksumMismatch
	}
	return nil
}

func (s *Store) Head(ctx context.Context, key string) (objectstore.ObjectInfo, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	o, err := s.loadCommitted(ctx, key)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return o.info(), nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := objectstore.ValidateKey(key); err != nil {
		return err
	}
	tx, err := s.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return err
	}
	var id string
	var size int64
	var locked bool
	err = tx.QueryRowContext(ctx, `SELECT id::text, size, locked FROM `+s.t("BlobObjects")+` WHERE key = $1 AND status = 'committed'`, key).Scan(&id, &size, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if locked {
		return fmt.Errorf("%w: %s", objectstore.ErrLocked, key)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+s.t("BlobObjects")+` SET status = 'deleted', deleted_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	if ws := workspaceOf(key); ws != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE `+s.t("BlobUsage")+` SET bytes = GREATEST(bytes - $2, 0) WHERE workspace_id = $1`, ws, size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) List(ctx context.Context, prefix string, opts objectstore.ListOptions) (objectstore.ListPage, error) {
	if err := objectstore.ValidatePrefix(prefix); err != nil {
		return objectstore.ListPage{}, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.cfg.DB.QueryContext(ctx, `SELECT `+objCols+` FROM `+s.t("BlobObjects")+`
		WHERE status = 'committed' AND key LIKE $1 ESCAPE '\' AND key > $2 ORDER BY key LIMIT $3`,
		likeEscape(prefix)+"%", opts.After, limit+1)
	if err != nil {
		return objectstore.ListPage{}, err
	}
	defer func() { _ = rows.Close() }()
	var page objectstore.ListPage
	for rows.Next() {
		o, err := scanObj(rows)
		if err != nil {
			return objectstore.ListPage{}, err
		}
		page.Objects = append(page.Objects, o.info())
	}
	if len(page.Objects) > limit {
		page.Objects = page.Objects[:limit]
		page.Next = page.Objects[limit-1].Key
	}
	return page, rows.Err()
}

// Copy re-encrypts under the destination object's own key (chunks are bound to
// their object id), streaming with bounded memory.
func (s *Store) Copy(ctx context.Context, src, dst string) (objectstore.ObjectInfo, error) {
	for _, k := range []string{src, dst} {
		if err := objectstore.ValidateKey(k); err != nil {
			return objectstore.ObjectInfo{}, err
		}
	}
	rc, info, err := s.Get(ctx, src, nil)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	defer func() { _ = rc.Close() }()
	return s.Put(ctx, dst, rc, objectstore.PutOptions{MediaType: info.MediaType, Metadata: info.Metadata})
}

func (s *Store) Presign(ctx context.Context, key string, opts objectstore.PresignOptions) (string, error) {
	if _, err := s.Head(ctx, key); err != nil {
		return "", err
	}
	return objectstore.SignDownload(key, opts, s.cfg.PresignSecret, s.now())
}

// ── multipart ────────────────────────────────────────────────────────────────

const partStride = int64(1_000_000) // chunk seq space reserved per part

func newUploadID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) BeginMultipart(ctx context.Context, key string, opts objectstore.PutOptions) (objectstore.UploadID, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return "", err
	}
	id := newUploadID()
	if _, _, err := s.begin(ctx, key, opts, id); err != nil {
		return "", err
	}
	return objectstore.UploadID(id), nil
}

type upRow struct {
	objID, key string
	wrapped    string
	workspace  string
	chunkSize  int
	opts       objectstore.PutOptions
}

func (s *Store) loadUpload(ctx context.Context, id objectstore.UploadID) (upRow, error) {
	var u upRow
	var md, po []byte
	var media sql.NullString
	var locked bool
	var exp sql.NullTime
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT id::text, key, wrapped_key, workspace_id, chunk_size, metadata, put_opts, media_type, locked, expires_at
		FROM `+s.t("BlobObjects")+` WHERE upload_id = $1 AND status = 'writing'`, string(id)).
		Scan(&u.objID, &u.key, &u.wrapped, &u.workspace, &u.chunkSize, &md, &po, &media, &locked, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return upRow{}, objectstore.ErrUploadNotFound
	}
	if err != nil {
		return upRow{}, err
	}
	u.opts = objectstore.PutOptions{MediaType: media.String, Locked: locked}
	if exp.Valid {
		u.opts.ExpiresAt = exp.Time
	}
	_ = json.Unmarshal(md, &u.opts.Metadata)
	var p struct {
		IfNotExists bool   `json:"ifNotExists"`
		Expected    string `json:"expected"`
	}
	_ = json.Unmarshal(po, &p)
	u.opts.IfNotExists, u.opts.ExpectedSHA256 = p.IfNotExists, p.Expected
	return u, nil
}

func (s *Store) UploadPart(ctx context.Context, id objectstore.UploadID, number int, r io.Reader) (objectstore.PartInfo, error) {
	u, err := s.loadUpload(ctx, id)
	if err != nil {
		return objectstore.PartInfo{}, err
	}
	if number < 1 || number > 10_000 {
		return objectstore.PartInfo{}, fmt.Errorf("%w: part number %d outside 1..10000", objectstore.ErrInvalidPart, number)
	}
	dataKey, err := unwrapKey(s.cfg.MasterKey, u.workspace, u.objID, u.wrapped)
	if err != nil {
		return objectstore.PartInfo{}, err
	}
	first := int64(number) * partStride
	// A retry replaces the part: clear its chunk range and descriptor first.
	if _, err := s.cfg.DB.ExecContext(ctx, `DELETE FROM `+s.t("BlobChunks")+` WHERE object_id = $1 AND seq >= $2 AND seq < $3`, u.objID, first, first+partStride); err != nil {
		return objectstore.PartInfo{}, err
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `DELETE FROM `+s.t("BlobParts")+` WHERE object_id = $1 AND part = $2`, u.objID, number); err != nil {
		return objectstore.PartInfo{}, err
	}
	size, sum, _, err := s.writeChunks(ctx, u.objID, dataKey, r, first, s.cfg.MaxObjectSize)
	if err != nil {
		_, _ = s.cfg.DB.ExecContext(context.Background(), `DELETE FROM `+s.t("BlobChunks")+` WHERE object_id = $1 AND seq >= $2 AND seq < $3`, u.objID, first, first+partStride)
		return objectstore.PartInfo{}, err
	}
	sh := hex.EncodeToString(sum[:])
	if _, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO `+s.t("BlobParts")+` (object_id, part, size, sha256) VALUES ($1,$2,$3,$4)`, u.objID, number, size, sh); err != nil {
		return objectstore.PartInfo{}, objectstore.ErrUploadNotFound // aborted under us
	}
	return objectstore.PartInfo{Number: number, Size: size, SHA256: sh}, nil
}

func (s *Store) CompleteMultipart(ctx context.Context, id objectstore.UploadID, parts []objectstore.PartInfo) (objectstore.ObjectInfo, error) {
	u, err := s.loadUpload(ctx, id)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if len(parts) == 0 {
		return objectstore.ObjectInfo{}, fmt.Errorf("%w: no parts", objectstore.ErrInvalidPart)
	}
	var layout []segment
	var total int64
	last := 0
	for _, p := range parts {
		if p.Number <= last {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: parts must be listed in strictly ascending order", objectstore.ErrInvalidPart)
		}
		last = p.Number
		var size int64
		var sh string
		err := s.cfg.DB.QueryRowContext(ctx, `SELECT size, sha256 FROM `+s.t("BlobParts")+` WHERE object_id = $1 AND part = $2`, u.objID, p.Number).Scan(&size, &sh)
		if errors.Is(err, sql.ErrNoRows) {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: part %d was never uploaded", objectstore.ErrInvalidPart, p.Number)
		}
		if err != nil {
			return objectstore.ObjectInfo{}, err
		}
		if size != p.Size || !strings.EqualFold(sh, p.SHA256) {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: part %d does not match what was uploaded", objectstore.ErrInvalidPart, p.Number)
		}
		total += size
		if total > s.cfg.MaxObjectSize {
			return objectstore.ObjectInfo{}, objectstore.ErrTooLarge
		}
		layout = append(layout, segment{FirstSeq: int64(p.Number) * partStride, Size: size})
	}
	// Chunks of parts that were uploaded but not listed are dropped.
	keep := make([]int64, 0, len(parts))
	for _, p := range parts {
		keep = append(keep, int64(p.Number))
	}
	// Whole-object SHA-256: parts' hashes do not compose, so stream the
	// assembled plaintext once (bounded memory).
	o := objRow{id: u.objID, workspace: u.workspace, key: u.key, wrapped: u.wrapped, chunkSize: u.chunkSize, layout: layout, size: total}
	dataKey, err := unwrapKey(s.cfg.MasterKey, o.workspace, o.id, o.wrapped)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	aead, err := newAEAD(dataKey)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	rd := &reader{s: s, ctx: ctx, o: o, aead: aead, off: 0, remain: total}
	if total == 0 {
		rd.release()
	}
	h := sha256.New()
	_, err = io.Copy(h, rd)
	rd.release()
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if _, err := s.cfg.DB.ExecContext(ctx, `DELETE FROM `+s.t("BlobChunks")+` WHERE object_id = $1 AND (seq / $2) <> ALL($3::bigint[])`, u.objID, partStride, int64SliceToArray(keep)); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	info, err := s.commit(ctx, u.objID, u.key, total, hex.EncodeToString(h.Sum(nil)), layout, u.opts)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return info, nil
}

func int64SliceToArray(v []int64) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprint(n)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (s *Store) AbortMultipart(ctx context.Context, id objectstore.UploadID) error {
	_, err := s.cfg.DB.ExecContext(ctx, `DELETE FROM `+s.t("BlobObjects")+` WHERE upload_id = $1 AND status = 'writing'`, string(id))
	return err
}

// MarkSpent implements httpdl.Spent: it records a single-use download token as
// used and reports true exactly once per id, across every instance sharing the
// database.
func (s *Store) MarkSpent(ctx context.Context, id string, expires time.Time) (bool, error) {
	res, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO `+s.t("BlobSpentTokens")+` (id, expires_at) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, id, expires.UTC())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ── lifecycle ────────────────────────────────────────────────────────────────

// SweepResult counts what a Sweep removed.
type SweepResult struct{ Abandoned, Purged, Expired int64 }

// Sweep applies lifecycle rules: it removes unfinished (`writing`) objects and
// multipart uploads older than WritingTTL — the leftovers of killed writers —
// purges deleted/replaced objects past DeletedGrace, and retires committed
// objects whose ExpiresAt has passed (locked ones are never expired).
func (s *Store) Sweep(ctx context.Context) (SweepResult, error) {
	var r SweepResult
	exec := func(dst *int64, q string, args ...any) error {
		res, err := s.cfg.DB.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		*dst, _ = res.RowsAffected()
		return nil
	}
	now := s.now()
	if err := exec(&r.Abandoned, `DELETE FROM `+s.t("BlobObjects")+` WHERE status = 'writing' AND created_at < $1`, now.Add(-s.cfg.WritingTTL)); err != nil {
		return r, err
	}
	if err := exec(&r.Purged, `DELETE FROM `+s.t("BlobObjects")+` WHERE status = 'deleted' AND deleted_at < $1`, now.Add(-s.cfg.DeletedGrace)); err != nil {
		return r, err
	}
	_, _ = s.cfg.DB.ExecContext(ctx, `DELETE FROM `+s.t("BlobSpentTokens")+` WHERE expires_at < $1`, now.Add(-time.Hour))
	// Expire: same accounting as Delete, per object.
	rows, err := s.cfg.DB.QueryContext(ctx, `SELECT key FROM `+s.t("BlobObjects")+` WHERE status = 'committed' AND NOT locked AND expires_at IS NOT NULL AND expires_at < $1 LIMIT 1000`, now)
	if err != nil {
		return r, err
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	_ = rows.Close()
	for _, k := range keys {
		if err := s.Delete(ctx, k); err == nil {
			r.Expired++
		}
	}
	return r, nil
}
