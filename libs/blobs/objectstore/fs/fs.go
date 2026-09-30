// Package fs is the local-filesystem ObjectStore driver (PLAN M1-05): for
// tests, offline demos and single-machine development. It is not meant to be
// shared between processes (locking is in-process). The Postgres driver is the
// production default (M1-05a).
//
// Layout under the root directory:
//
//	meta/<sha256(key)>         JSON describing the committed object; its
//	                           atomic rename IS the commit point
//	data/<id>                  immutable content files, referenced by meta
//	tmp/                       in-flight writes, never visible
//	mp/<uploadId>/             multipart state (upload.json, part-N, part-N.json)
//
// Content files are never modified after they are written; an overwrite
// writes a new data file and atomically swaps the meta, so a reader that
// already opened the old object keeps reading the old bytes intact.
package fs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
)

// Config configures the driver.
type Config struct {
	// MaxObjectSize caps one object; 0 means objectstore.DefaultMaxObjectSize.
	MaxObjectSize int64
	// PresignSecret signs download URLs (>= 16 bytes). Without it Presign fails.
	PresignSecret []byte
	// Now defaults to time.Now.
	Now func() time.Time
}

// Store implements objectstore.ObjectStore on a directory.
type Store struct {
	root string
	cfg  Config

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var _ objectstore.ObjectStore = (*Store)(nil)

// New creates (if needed) and opens a store rooted at dir.
func New(dir string, cfg Config) (*Store, error) {
	if cfg.MaxObjectSize <= 0 {
		cfg.MaxObjectSize = objectstore.DefaultMaxObjectSize
	}
	for _, d := range []string{"meta", "data", "tmp", "mp"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{root: dir, cfg: cfg, locks: map[string]*sync.Mutex{}}, nil
}

func (s *Store) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

type meta struct {
	Key       string            `json:"key"`
	DataFile  string            `json:"dataFile"`
	Size      int64             `json:"size"`
	SHA256    string            `json:"sha256"`
	MediaType string            `json:"mediaType,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (m meta) info() objectstore.ObjectInfo {
	return objectstore.ObjectInfo{Key: m.Key, Size: m.Size, SHA256: m.SHA256, MediaType: m.MediaType, CreatedAt: m.CreatedAt, Metadata: m.Metadata}
}

func keyHash(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }

func (s *Store) metaPath(key string) string { return filepath.Join(s.root, "meta", keyHash(key)) }
func (s *Store) dataPath(id string) string  { return filepath.Join(s.root, "data", id) }

// keyLock serialises commits/deletes on one key (in-process).
func (s *Store) keyLock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	return l
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
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

func readMeta(path string) (meta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return meta{}, err
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return meta{}, err
	}
	return m, nil
}

func (s *Store) loadMeta(key string) (meta, error) {
	m, err := readMeta(s.metaPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return meta{}, fmt.Errorf("%w: %s", objectstore.ErrNotFound, key)
	}
	return m, err
}

// ctxReader fails a read as soon as ctx is done.
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

// stage copies r into a temp file, hashing and enforcing the size cap, and
// returns its path, size and SHA-256. The caller owns (removes) the file.
func (s *Store) stage(ctx context.Context, r io.Reader, limit int64) (path string, size int64, sum string, err error) {
	f, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "w-*")
	if err != nil {
		return "", 0, "", err
	}
	path = f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(ctxReader{ctx, r}, limit+1))
	if err != nil {
		return "", 0, "", err
	}
	if n > limit {
		return "", 0, "", objectstore.ErrTooLarge
	}
	if err = f.Sync(); err != nil {
		return "", 0, "", err
	}
	return path, n, hex.EncodeToString(h.Sum(nil)), nil
}

// commit publishes a staged file as key. On success the staged file is moved
// into data/; on failure it is left for the caller to remove.
func (s *Store) commit(key, staged string, size int64, sum string, opts objectstore.PutOptions) (objectstore.ObjectInfo, error) {
	if opts.ExpectedSHA256 != "" && !strings.EqualFold(opts.ExpectedSHA256, sum) {
		return objectstore.ObjectInfo{}, objectstore.ErrChecksumMismatch
	}
	l := s.keyLock(key)
	l.Lock()
	defer l.Unlock()

	old, oldErr := readMeta(s.metaPath(key))
	if opts.IfNotExists && oldErr == nil {
		return objectstore.ObjectInfo{}, fmt.Errorf("%w: %s", objectstore.ErrExists, key)
	}

	id := randomID()
	if err := os.Rename(staged, s.dataPath(id)); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	m := meta{Key: key, DataFile: id, Size: size, SHA256: sum, MediaType: opts.MediaType, CreatedAt: s.now(), Metadata: lowerKeys(opts.Metadata)}
	if err := s.writeMeta(key, m); err != nil {
		_ = os.Remove(s.dataPath(id))
		return objectstore.ObjectInfo{}, err
	}
	if oldErr == nil {
		_ = os.Remove(s.dataPath(old.DataFile)) // open readers keep their fd
	}
	return m.info(), nil
}

// writeMeta atomically replaces the meta file: this rename is the commit point.
func (s *Store) writeMeta(key string, m meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "m-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	_ = tmp.Close()
	if err := os.Rename(tmp.Name(), s.metaPath(key)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts objectstore.PutOptions) (objectstore.ObjectInfo, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	staged, size, sum, err := s.stage(ctx, r, s.cfg.MaxObjectSize)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	info, err := s.commit(key, staged, size, sum, opts)
	if err != nil {
		_ = os.Remove(staged)
		return objectstore.ObjectInfo{}, err
	}
	return info, nil
}

type fileReader struct {
	*os.File
	r io.Reader
}

func (f fileReader) Read(p []byte) (int, error) { return f.r.Read(p) }

func (s *Store) Get(ctx context.Context, key string, rng *objectstore.Range) (io.ReadCloser, objectstore.ObjectInfo, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	var (
		m meta
		f *os.File
	)
	for attempt := 0; ; attempt++ { // an overwrite may swap the data file between our two reads
		var err error
		if m, err = s.loadMeta(key); err != nil {
			return nil, objectstore.ObjectInfo{}, err
		}
		if f, err = os.Open(s.dataPath(m.DataFile)); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) || attempt >= 3 {
			return nil, objectstore.ObjectInfo{}, err
		}
	}
	start, length, err := resolveRange(rng, m.Size)
	if err != nil {
		_ = f.Close()
		return nil, objectstore.ObjectInfo{}, err
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, objectstore.ObjectInfo{}, err
		}
	}
	return fileReader{File: f, r: io.LimitReader(ctxReader{ctx, f}, length)}, m.info(), nil
}

// resolveRange turns a Range into (start, length) over an object of size bytes.
func resolveRange(rng *objectstore.Range, size int64) (start, length int64, err error) {
	if rng == nil {
		return 0, size, nil
	}
	unsat := fmt.Errorf("%w: size %d", objectstore.ErrRangeNotSatisfiable, size)
	switch {
	case rng.Suffix > 0:
		if size == 0 {
			return 0, 0, unsat
		}
		n := rng.Suffix
		if n > size {
			n = size
		}
		return size - n, n, nil
	case rng.Start < 0 || rng.Start >= size:
		return 0, 0, unsat
	case rng.OpenEnd:
		return rng.Start, size - rng.Start, nil
	case rng.End < rng.Start:
		return 0, 0, unsat
	}
	end := rng.End
	if end >= size {
		end = size - 1
	}
	return rng.Start, end - rng.Start + 1, nil
}

func (s *Store) Head(ctx context.Context, key string) (objectstore.ObjectInfo, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	m, err := s.loadMeta(key)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return m.info(), nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := objectstore.ValidateKey(key); err != nil {
		return err
	}
	l := s.keyLock(key)
	l.Lock()
	defer l.Unlock()
	m, err := readMeta(s.metaPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(s.metaPath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(s.dataPath(m.DataFile))
	return nil
}

func (s *Store) List(ctx context.Context, prefix string, opts objectstore.ListOptions) (objectstore.ListPage, error) {
	if err := objectstore.ValidatePrefix(prefix); err != nil {
		return objectstore.ListPage{}, err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "meta"))
	if err != nil {
		return objectstore.ListPage{}, err
	}
	var metas []meta
	for _, e := range entries {
		m, err := readMeta(filepath.Join(s.root, "meta", e.Name()))
		if err != nil {
			continue // removed between ReadDir and read
		}
		if strings.HasPrefix(m.Key, prefix) && m.Key > opts.After {
			metas = append(metas, m)
		}
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Key < metas[j].Key })
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	var page objectstore.ListPage
	if len(metas) > limit {
		metas = metas[:limit]
		page.Next = metas[len(metas)-1].Key
	}
	for _, m := range metas {
		page.Objects = append(page.Objects, m.info())
	}
	return page, nil
}

func (s *Store) Copy(ctx context.Context, src, dst string) (objectstore.ObjectInfo, error) {
	for _, k := range []string{src, dst} {
		if err := objectstore.ValidateKey(k); err != nil {
			return objectstore.ObjectInfo{}, err
		}
	}
	m, err := s.loadMeta(src)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	// A hard link (independent name for the same immutable bytes) where the
	// filesystem allows it, otherwise a real copy.
	staged := filepath.Join(s.root, "tmp", "c-"+randomID())
	if err := os.Link(s.dataPath(m.DataFile), staged); err != nil {
		in, oerr := os.Open(s.dataPath(m.DataFile))
		if oerr != nil {
			return objectstore.ObjectInfo{}, oerr
		}
		defer func() { _ = in.Close() }()
		out, cerr := os.Create(staged)
		if cerr != nil {
			return objectstore.ObjectInfo{}, cerr
		}
		_, cperr := io.Copy(out, in)
		_ = out.Close()
		if cperr != nil {
			_ = os.Remove(staged)
			return objectstore.ObjectInfo{}, cperr
		}
	}
	info, err := s.commit(dst, staged, m.Size, m.SHA256, objectstore.PutOptions{MediaType: m.MediaType, Metadata: m.Metadata})
	if err != nil {
		_ = os.Remove(staged)
		return objectstore.ObjectInfo{}, err
	}
	return info, nil
}

func (s *Store) Presign(ctx context.Context, key string, opts objectstore.PresignOptions) (string, error) {
	if _, err := s.Head(ctx, key); err != nil {
		return "", err
	}
	return objectstore.SignDownload(key, opts, s.cfg.PresignSecret, s.now())
}

// ── multipart ────────────────────────────────────────────────────────────────

const maxParts = 10_000

type upload struct {
	Key  string                 `json:"key"`
	Opts objectstore.PutOptions `json:"opts"`
}

type partMeta struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// uploadDir validates an upload id (32 lowercase hex chars — which also makes
// path traversal impossible) and returns its directory.
func (s *Store) uploadDir(id objectstore.UploadID) (string, error) {
	if len(id) != 32 {
		return "", objectstore.ErrUploadNotFound
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", objectstore.ErrUploadNotFound
		}
	}
	dir := filepath.Join(s.root, "mp", string(id))
	if _, err := os.Stat(filepath.Join(dir, "upload.json")); err != nil {
		return "", objectstore.ErrUploadNotFound
	}
	return dir, nil
}

func (s *Store) BeginMultipart(ctx context.Context, key string, opts objectstore.PutOptions) (objectstore.UploadID, error) {
	if err := objectstore.ValidateKey(key); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := randomID()
	dir := filepath.Join(s.root, "mp", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b, _ := json.Marshal(upload{Key: key, Opts: opts})
	if err := os.WriteFile(filepath.Join(dir, "upload.json"), b, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return objectstore.UploadID(id), nil
}

func (s *Store) UploadPart(ctx context.Context, id objectstore.UploadID, number int, r io.Reader) (objectstore.PartInfo, error) {
	dir, err := s.uploadDir(id)
	if err != nil {
		return objectstore.PartInfo{}, err
	}
	if number < 1 || number > maxParts {
		return objectstore.PartInfo{}, fmt.Errorf("%w: part number %d outside 1..%d", objectstore.ErrInvalidPart, number, maxParts)
	}
	staged, size, sum, err := s.stage(ctx, r, s.cfg.MaxObjectSize)
	if err != nil {
		return objectstore.PartInfo{}, err
	}
	pm, _ := json.Marshal(partMeta{Size: size, SHA256: sum})
	// A retry replaces the part: data first, then its descriptor.
	if err := os.Rename(staged, filepath.Join(dir, fmt.Sprintf("part-%d", number))); err != nil {
		_ = os.Remove(staged)
		return objectstore.PartInfo{}, objectstore.ErrUploadNotFound // the upload was aborted under us
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("part-%d.json", number)), pm, 0o644); err != nil {
		return objectstore.PartInfo{}, err
	}
	return objectstore.PartInfo{Number: number, Size: size, SHA256: sum}, nil
}

func (s *Store) CompleteMultipart(ctx context.Context, id objectstore.UploadID, parts []objectstore.PartInfo) (objectstore.ObjectInfo, error) {
	dir, err := s.uploadDir(id)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "upload.json"))
	if err != nil {
		return objectstore.ObjectInfo{}, objectstore.ErrUploadNotFound
	}
	var up upload
	if err := json.Unmarshal(b, &up); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if len(parts) == 0 {
		return objectstore.ObjectInfo{}, fmt.Errorf("%w: no parts", objectstore.ErrInvalidPart)
	}
	last := 0
	var total int64
	for _, p := range parts {
		if p.Number <= last {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: parts must be listed in strictly ascending order", objectstore.ErrInvalidPart)
		}
		last = p.Number
		raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("part-%d.json", p.Number)))
		if err != nil {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: part %d was never uploaded", objectstore.ErrInvalidPart, p.Number)
		}
		var pm partMeta
		if json.Unmarshal(raw, &pm) != nil || pm.Size != p.Size || !strings.EqualFold(pm.SHA256, p.SHA256) {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: part %d does not match what was uploaded", objectstore.ErrInvalidPart, p.Number)
		}
		total += pm.Size
		if total > s.cfg.MaxObjectSize {
			return objectstore.ObjectInfo{}, objectstore.ErrTooLarge
		}
	}
	// Assemble in part-number order into a staged file.
	out, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "w-*")
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	staged := out.Name()
	h := sha256.New()
	fail := func(err error) (objectstore.ObjectInfo, error) {
		_ = out.Close()
		_ = os.Remove(staged)
		return objectstore.ObjectInfo{}, err
	}
	for _, p := range parts {
		in, err := os.Open(filepath.Join(dir, fmt.Sprintf("part-%d", p.Number)))
		if err != nil {
			return fail(fmt.Errorf("%w: part %d", objectstore.ErrInvalidPart, p.Number))
		}
		_, cerr := io.Copy(io.MultiWriter(out, h), ctxReader{ctx, in})
		_ = in.Close()
		if cerr != nil {
			return fail(cerr)
		}
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	_ = out.Close()
	info, err := s.commit(up.Key, staged, total, hex.EncodeToString(h.Sum(nil)), up.Opts)
	if err != nil {
		_ = os.Remove(staged)
		return objectstore.ObjectInfo{}, err
	}
	_ = os.RemoveAll(dir)
	return info, nil
}

func (s *Store) AbortMultipart(ctx context.Context, id objectstore.UploadID) error {
	dir, err := s.uploadDir(id)
	if errors.Is(err, objectstore.ErrUploadNotFound) {
		return nil // idempotent
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// SweepUploads removes multipart uploads untouched for longer than olderThan
// (an abandoned upload must not hold disk forever) and returns how many.
func (s *Store) SweepUploads(olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "mp"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || s.now().Sub(fi.ModTime()) < olderThan {
			continue
		}
		if os.RemoveAll(filepath.Join(s.root, "mp", e.Name())) == nil {
			n++
		}
	}
	return n, nil
}
