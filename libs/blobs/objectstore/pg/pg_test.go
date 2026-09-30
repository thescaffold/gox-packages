package pg_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/pg"
	"github.com/thescaffold/gox-packages/libs/blobs/objectstore/storetest"
)

var (
	secret    = []byte("pg-driver-test-secret-0123456789")
	masterKey = []byte("0123456789abcdef0123456789abcdef")
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BLOBS_TEST_DB_URL")
	if dsn == "" {
		t.Skip("BLOBS_TEST_DB_URL not set; skipping Postgres driver tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newStore builds a store in its own throwaway schema.
func newStore(t *testing.T, db *sql.DB, mod func(*pg.Config)) (*pg.Store, string) {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "bt_" + hex.EncodeToString(b)
	if err := pg.Migrate(context.Background(), db, schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	cfg := pg.Config{DB: db, Schema: schema, MasterKey: masterKey, PresignSecret: secret, MaxObjectSize: 8 << 20, ChunkSize: 64 << 10}
	if mod != nil {
		mod(&cfg)
	}
	s, err := pg.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, schema
}

// The Postgres driver against the shared contract suite (PLAN M1-05a). A small
// chunk size forces every multi-chunk, multi-segment and range path.
func TestContract(t *testing.T) {
	db := testDB(t)
	storetest.Run(t, storetest.Config{
		New:           func(t *testing.T) objectstore.ObjectStore { s, _ := newStore(t, db, nil); return s },
		PresignSecret: secret,
		MaxObjectSize: 8 << 20,
		LargeBytes:    8 << 20,
	})
}

func TestMigrate_IsIdempotentAndConcurrentSafe(t *testing.T) {
	db := testDB(t)
	_, schema := newStore(t, db, nil)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pg.Migrate(context.Background(), db, schema); err != nil {
				t.Errorf("concurrent/repeat migrate: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := pg.Migrate(context.Background(), db, "Bad-Schema; DROP"); err == nil {
		t.Fatal("an unsafe schema name was accepted")
	}
}

func TestStoredBytesAreEncrypted_AndTamperingIsDetected(t *testing.T) {
	db := testDB(t)
	s, schema := newStore(t, db, nil)
	ctx := context.Background()
	secretText := strings.Repeat("TOP-SECRET-PLAINTEXT ", 5000) // 105 KB -> 2 chunks
	if _, err := s.Put(ctx, "ws/a/enc", strings.NewReader(secretText), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	rows, _ := db.Query(`SELECT data FROM ` + schema + `."BlobChunks"`)
	n := 0
	for rows.Next() {
		var d []byte
		_ = rows.Scan(&d)
		raw = append(raw, d...)
		n++
	}
	_ = rows.Close()
	if n < 2 || bytes.Contains(raw, []byte("TOP-SECRET")) {
		t.Fatalf("%d chunks; plaintext visible at rest: %v", n, bytes.Contains(raw, []byte("TOP-SECRET")))
	}
	var wrapped string
	_ = db.QueryRow(`SELECT wrapped_key FROM ` + schema + `."BlobObjects" LIMIT 1`).Scan(&wrapped)
	if wrapped == "" || strings.Contains(wrapped, "0123456789abcdef") {
		t.Fatal("wrapped key missing or leaks the master key")
	}
	if err := s.Verify(ctx, "ws/a/enc"); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// A flipped bit in a stored chunk is detected on read.
	if _, err := db.Exec(`UPDATE ` + schema + `."BlobChunks" SET data = set_byte(data, 40, get_byte(data, 40) # 1) WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	rc, _, err := s.Get(ctx, "ws/a/enc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err == nil {
		t.Fatal("a tampered chunk was served")
	}
	_ = rc.Close()
}

func TestChunkSwapBetweenObjectsIsDetected(t *testing.T) {
	db := testDB(t)
	s, schema := newStore(t, db, nil)
	ctx := context.Background()
	_, _ = s.Put(ctx, "ws/a/one", strings.NewReader(strings.Repeat("1", 1000)), objectstore.PutOptions{})
	_, _ = s.Put(ctx, "ws/a/two", strings.NewReader(strings.Repeat("2", 1000)), objectstore.PutOptions{})
	// Copy object "two"'s chunk bytes over "one"'s: same length, valid ciphertext, wrong object.
	if _, err := db.Exec(`UPDATE ` + schema + `."BlobChunks" c SET data = t.data FROM ` + schema + `."BlobChunks" t, ` + schema + `."BlobObjects" o1, ` + schema + `."BlobObjects" o2
		WHERE o1.key = 'ws/a/one' AND o2.key = 'ws/a/two' AND c.object_id = o1.id AND t.object_id = o2.id AND c.seq = 0 AND t.seq = 0`); err != nil {
		t.Fatal(err)
	}
	rc, _, _ := s.Get(ctx, "ws/a/one", nil)
	if _, err := io.ReadAll(rc); err == nil {
		t.Fatal("a chunk moved from another object was accepted")
	}
	_ = rc.Close()
}

func TestWrongMasterKeyCannotRead(t *testing.T) {
	db := testDB(t)
	s, schema := newStore(t, db, nil)
	_, _ = s.Put(context.Background(), "ws/a/k", strings.NewReader("data"), objectstore.PutOptions{})
	other, _ := pg.New(pg.Config{DB: db, Schema: schema, MasterKey: []byte("ffffffffffffffffffffffffffffffff"), PresignSecret: secret})
	if _, _, err := other.Get(context.Background(), "ws/a/k", nil); err == nil {
		t.Fatal("an object was readable with the wrong master key")
	}
	// Keys are per workspace: an object stored under another workspace's id
	// cannot be unwrapped by moving its row.
	_, _ = s.Put(context.Background(), "ws/wsB/k", strings.NewReader("b"), objectstore.PutOptions{})
	if _, err := db.Exec(`UPDATE ` + schema + `."BlobObjects" SET wrapped_key = (SELECT wrapped_key FROM ` + schema + `."BlobObjects" WHERE key = 'ws/a/k') WHERE key = 'ws/wsB/k'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(context.Background(), "ws/wsB/k", nil); err == nil {
		t.Fatal("a wrapped key from another workspace/object unwrapped")
	}
}

func TestKilledUploadLeavesNoVisibleObject_AndSweepRemovesIt(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	clock := &now
	s, schema := newStore(t, db, func(c *pg.Config) { c.Now = func() time.Time { return *clock } })
	ctx := context.Background()

	// Simulate a writer killed mid-upload: a `writing` object with chunks and no commit.
	_, err := s.Put(ctx, "ws/a/killed", &brokenAfter{n: 300_000}, objectstore.PutOptions{})
	if err == nil {
		t.Fatal("expected the broken reader to fail the Put")
	}
	if _, err := s.Head(ctx, "ws/a/killed"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("a killed upload is visible: %v", err)
	}
	// The kill -9 case: the cleanup in Put never runs. Recreate that state directly.
	id, _ := s.BeginMultipart(ctx, "ws/a/orphan", objectstore.PutOptions{})
	_, _ = s.UploadPart(ctx, id, 1, strings.NewReader(strings.Repeat("x", 200_000)))
	if n := count(t, db, schema, `status = 'writing'`); n != 1 {
		t.Fatalf("expected 1 writing object, got %d", n)
	}
	if _, err := s.Head(ctx, "ws/a/orphan"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("an unfinished upload is visible")
	}
	// Not yet stale: untouched. After the TTL: swept, chunks and all.
	if r, _ := s.Sweep(ctx); r.Abandoned != 0 {
		t.Fatalf("a fresh upload was swept: %+v", r)
	}
	*clock = now.Add(2 * time.Hour)
	r, err := s.Sweep(ctx)
	if err != nil || r.Abandoned != 1 {
		t.Fatalf("sweep = %+v, %v; want the orphan removed", r, err)
	}
	var chunks int
	_ = db.QueryRow(`SELECT count(*) FROM ` + schema + `."BlobChunks"`).Scan(&chunks)
	if chunks != 0 {
		t.Fatalf("%d orphaned chunks survived the sweep", chunks)
	}
}

type brokenAfter struct{ n, at int }

func (b *brokenAfter) Read(p []byte) (int, error) {
	if b.at >= b.n {
		return 0, errors.New("connection reset")
	}
	k := len(p)
	if b.n-b.at < k {
		k = b.n - b.at
	}
	b.at += k
	return k, nil
}

func count(t *testing.T, db *sql.DB, schema, where string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + schema + `."BlobObjects" WHERE ` + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOverwriteAndDeleteArePurgedAfterGrace(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	clock := &now
	s, schema := newStore(t, db, func(c *pg.Config) { c.Now = func() time.Time { return *clock } })
	ctx := context.Background()
	_, _ = s.Put(ctx, "ws/a/k", strings.NewReader(strings.Repeat("a", 100_000)), objectstore.PutOptions{})
	_, _ = s.Put(ctx, "ws/a/k", strings.NewReader("b"), objectstore.PutOptions{})
	_ = s.Delete(ctx, "ws/a/k")
	if n := count(t, db, schema, `status = 'deleted'`); n != 2 {
		t.Fatalf("%d deleted rows, want 2 (kept briefly for in-flight readers)", n)
	}
	if r, _ := s.Sweep(ctx); r.Purged != 0 {
		t.Fatalf("purged inside the grace period: %+v", r)
	}
	*clock = now.Add(time.Hour)
	if r, _ := s.Sweep(ctx); r.Purged != 2 {
		t.Fatalf("purged %d, want 2", r.Purged)
	}
	var chunks int
	_ = db.QueryRow(`SELECT count(*) FROM ` + schema + `."BlobChunks"`).Scan(&chunks)
	if chunks != 0 {
		t.Fatalf("%d chunks left after purge", chunks)
	}
}

func TestLockedObjects_AreRefusedByTheDatabaseItself(t *testing.T) {
	db := testDB(t)
	s, schema := newStore(t, db, nil)
	ctx := context.Background()
	if _, err := s.Put(ctx, "ws/a/ev", strings.NewReader(strings.Repeat("evidence", 20_000)), objectstore.PutOptions{Locked: true}); err != nil {
		t.Fatal(err)
	}
	// Bypass the driver API entirely: raw SQL with ordinary credentials.
	for name, q := range map[string]string{
		"update object":  `UPDATE ` + schema + `."BlobObjects" SET size = 1 WHERE key = 'ws/a/ev'`,
		"delete object":  `DELETE FROM ` + schema + `."BlobObjects" WHERE key = 'ws/a/ev'`,
		"unlock":         `UPDATE ` + schema + `."BlobObjects" SET locked = false WHERE key = 'ws/a/ev'`,
		"update chunk":   `UPDATE ` + schema + `."BlobChunks" SET data = '\x00' WHERE seq = 0`,
		"delete chunks":  `DELETE FROM ` + schema + `."BlobChunks"`,
		"truncate-ish":   `DELETE FROM ` + schema + `."BlobChunks" WHERE seq >= 0`,
		"append a chunk": `INSERT INTO ` + schema + `."BlobChunks" (object_id, seq, data) SELECT id, 99, '\x00' FROM ` + schema + `."BlobObjects" WHERE key = 'ws/a/ev'`,
	} {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("%s: the database allowed a change to a locked object", name)
		}
	}
	if err := s.Verify(ctx, "ws/a/ev"); err != nil {
		t.Fatalf("the locked object no longer verifies: %v", err)
	}
	// Unlocked objects are unaffected by the triggers.
	_, _ = s.Put(ctx, "ws/a/free", strings.NewReader("x"), objectstore.PutOptions{})
	if err := s.Delete(ctx, "ws/a/free"); err != nil {
		t.Fatalf("the triggers broke ordinary deletes: %v", err)
	}
}

func TestQuotaAndPerObjectCap(t *testing.T) {
	db := testDB(t)
	s, _ := newStore(t, db, func(c *pg.Config) {
		c.Quota = func(_ context.Context, ws string) (int64, bool) {
			if ws == "small" {
				return 1000, true
			}
			return 0, false
		}
	})
	ctx := context.Background()
	put := func(k string, n int) error {
		_, err := s.Put(ctx, k, bytes.NewReader(make([]byte, n)), objectstore.PutOptions{})
		return err
	}
	if err := put("ws/small/a", 600); err != nil {
		t.Fatal(err)
	}
	if err := put("ws/small/b", 600); !errors.Is(err, objectstore.ErrQuotaExceeded) {
		t.Fatalf("over quota: %v", err)
	}
	if _, err := s.Head(ctx, "ws/small/b"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatal("an over-quota write left an object")
	}
	// Overwriting counts the REPLACEMENT, not the sum.
	if err := put("ws/small/a", 900); err != nil {
		t.Fatalf("overwrite within quota: %v", err)
	}
	// Deleting frees quota.
	_ = s.Delete(ctx, "ws/small/a")
	if err := put("ws/small/c", 1000); err != nil {
		t.Fatalf("after delete: %v", err)
	}
	// Other workspaces are unaffected.
	if err := put("ws/big/x", 5000); err != nil {
		t.Fatal(err)
	}
	if err := put("ws/big/huge", 9<<20); !errors.Is(err, objectstore.ErrTooLarge) {
		t.Fatalf("per-object cap: %v", err)
	}
}

func TestReadConcurrencyIsCapped_AndReleasedOnClose(t *testing.T) {
	db := testDB(t)
	s, _ := newStore(t, db, func(c *pg.Config) { c.MaxConcurrentReads = 2 })
	ctx := context.Background()
	_, _ = s.Put(ctx, "ws/a/k", strings.NewReader(strings.Repeat("z", 300_000)), objectstore.PutOptions{})
	r1, _, _ := s.Get(ctx, "ws/a/k", nil)
	r2, _, _ := s.Get(ctx, "ws/a/k", nil)
	blocked := make(chan struct{})
	go func() {
		r3, _, err := s.Get(ctx, "ws/a/k", nil)
		if err == nil {
			_ = r3.Close()
		}
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("a third concurrent download was admitted past the cap of 2")
	case <-time.After(300 * time.Millisecond):
	}
	_ = r1.Close()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("closing a reader did not free its slot")
	}
	// A waiter gives up when its context ends.
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	r3, _, _ := s.Get(ctx, "ws/a/k", nil)
	if _, _, err := s.Get(cctx, "ws/a/k", nil); err == nil {
		t.Fatal("a Get past the cap returned instead of waiting")
	}
	_ = r2.Close()
	_ = r3.Close()
	// Reading to EOF also frees the slot (no leak from a forgotten Close).
	for i := 0; i < 5; i++ {
		rc, _, err := s.Get(ctx, "ws/a/k", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, rc)
	}
}

// A paused client must not pin a database connection: with a pool of exactly
// ONE connection, an open, half-read download still lets other queries run.
func TestSlowReaderHoldsNoDatabaseConnectionBetweenBatches(t *testing.T) {
	db := testDB(t)
	db.SetMaxOpenConns(1)
	s, _ := newStore(t, db, nil)
	ctx := context.Background()
	if _, err := s.Put(ctx, "ws/a/k", bytes.NewReader(randb(2<<20)), objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	rc, _, err := s.Get(ctx, "ws/a/k", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if _, err := io.ReadFull(rc, make([]byte, 10)); err != nil { // reader is now mid-object, client "stalls"
		t.Fatal(err)
	}
	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := s.Head(hctx, "ws/a/k"); err != nil {
		t.Fatalf("a stalled download is holding the only database connection: %v", err)
	}
	// ...and the stalled reader resumes correctly afterwards.
	rest, err := io.ReadAll(rc)
	if err != nil || len(rest) != 2<<20-10 {
		t.Fatalf("resume: %d bytes, %v", len(rest), err)
	}
}

func TestPresignedURLRoundTripsThroughTheStore(t *testing.T) {
	db := testDB(t)
	s, _ := newStore(t, db, nil)
	ctx := context.Background()
	_, _ = s.Put(ctx, "ws/a/f.txt", strings.NewReader("hello"), objectstore.PutOptions{})
	u, err := s.Presign(ctx, "ws/a/f.txt", objectstore.PresignOptions{Filename: "f.txt"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := objectstore.VerifyDownload(u, secret, time.Now())
	if err != nil || c.Key != "ws/a/f.txt" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestMultiChunkRangesAcrossPartsAndChunks(t *testing.T) {
	db := testDB(t)
	s, _ := newStore(t, db, nil)
	ctx := context.Background()
	// Parts whose sizes are NOT multiples of the chunk size: the layout must map offsets across segments.
	parts := [][]byte{randb(150_000), randb(70_001), randb(200_000), randb(3)}
	id, _ := s.BeginMultipart(ctx, "ws/a/mp", objectstore.PutOptions{})
	var infos []objectstore.PartInfo
	var all []byte
	for i := len(parts) - 1; i >= 0; i-- { // upload in reverse
		pi, err := s.UploadPart(ctx, id, i+1, bytes.NewReader(parts[i]))
		if err != nil {
			t.Fatal(err)
		}
		infos = append([]objectstore.PartInfo{pi}, infos...)
	}
	for _, p := range parts {
		all = append(all, p...)
	}
	if _, err := s.CompleteMultipart(ctx, id, infos); err != nil {
		t.Fatal(err)
	}
	for _, r := range []objectstore.Range{
		{Start: 0, End: 10}, {Start: 149_990, End: 150_010}, {Start: 65_535, End: 65_537},
		{Start: 219_990, End: 220_010}, {Start: 420_000, OpenEnd: true}, {Suffix: 5}, {Start: 100_000, End: 300_000},
	} {
		r := r
		rc, _, err := s.Get(ctx, "ws/a/mp", &r)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		s0, l0, _ := objectstore.ResolveRange(&r, int64(len(all)))
		if err != nil || !bytes.Equal(got, all[s0:s0+l0]) {
			t.Errorf("range %+v: wrong bytes (%d vs %d) err=%v", r, len(got), l0, err)
		}
	}
	if err := s.Verify(ctx, "ws/a/mp"); err != nil {
		t.Fatal(err)
	}
}

func randb(n int) []byte { b := make([]byte, n); _, _ = rand.Read(b); return b }

type patternReader struct{ n, total int64 }

func (p *patternReader) Read(b []byte) (int, error) {
	if p.n >= p.total {
		return 0, io.EOF
	}
	k := int64(len(b))
	if p.total-p.n < k {
		k = p.total - p.n
	}
	for i := int64(0); i < k; i++ {
		b[i] = byte((p.n + i) * 131 >> 3)
	}
	p.n += k
	return int(k), nil
}

// PLAN M1-05a Done line: "a 1 GB object round-trips with bounded memory".
// Opt-in (BLOBS_TEST_BIG=1): it writes and reads a full GiB.
func TestOneGiBRoundTripWithBoundedMemory(t *testing.T) {
	if os.Getenv("BLOBS_TEST_BIG") == "" {
		t.Skip("set BLOBS_TEST_BIG=1 to run the 1 GiB round trip")
	}
	db := testDB(t)
	const total = int64(1) << 30
	s, _ := newStore(t, db, func(c *pg.Config) { c.MaxObjectSize = total + 1; c.ChunkSize = 2 << 20 })
	ctx := context.Background()

	var peak uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
			}
		}
	}()
	start := time.Now()
	info, err := s.Put(ctx, "ws/a/gib", &patternReader{total: total}, objectstore.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wrote := time.Since(start)
	rc, _, err := s.Get(ctx, "ws/a/gib", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n, err := io.Copy(h, rc)
	_ = rc.Close()
	close(stop)
	<-done
	if err != nil || n != total || hex.EncodeToString(h.Sum(nil)) != info.SHA256 {
		t.Fatalf("read back %d bytes, err=%v, hash ok=%v", n, err, hex.EncodeToString(h.Sum(nil)) == info.SHA256)
	}
	t.Logf("1 GiB: write %v, read+verify %v, peak heap %d MiB", wrote, time.Since(start)-wrote, peak>>20)
	if peak > 256<<20 {
		t.Fatalf("peak heap %d MiB while round-tripping 1 GiB — the driver is buffering", peak>>20)
	}
}

func TestMarkSpent_IsExactlyOnceAcrossConcurrentCallers(t *testing.T) {
	db := testDB(t)
	s, _ := newStore(t, db, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if first, err := s.MarkSpent(context.Background(), "token-1", time.Now().Add(time.Hour)); err == nil && first {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d callers were told they were first, want exactly 1", wins)
	}
	if first, _ := s.MarkSpent(context.Background(), "token-2", time.Now().Add(time.Hour)); !first {
		t.Fatal("a different token was reported as spent")
	}
}

// PLAN M1-05a Done line: "16 concurrent large downloads do not push CRUD p95
// past budget". A separate control-plane pool runs small queries while 16
// readers stream a large object through the BLOB pool; p95 is compared with
// the unloaded baseline. Opt-in (BLOBS_TEST_LOAD=1).
func TestSixteenConcurrentDownloadsDoNotStarveCRUD(t *testing.T) {
	if os.Getenv("BLOBS_TEST_LOAD") == "" {
		t.Skip("set BLOBS_TEST_LOAD=1 to run the load test")
	}
	blobDB := testDB(t)
	blobDB.SetMaxOpenConns(8) // the bounded blob pool
	ctrl, err := sql.Open("pgx", os.Getenv("BLOBS_TEST_DB_URL"))
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetMaxOpenConns(4)
	defer func() { _ = ctrl.Close() }()
	if _, err := ctrl.Exec(`CREATE TABLE IF NOT EXISTS load_probe (id serial PRIMARY KEY, v text)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = ctrl.Exec(`DROP TABLE IF EXISTS load_probe`) })

	s, _ := newStore(t, blobDB, func(c *pg.Config) { c.MaxObjectSize = 100 << 20; c.ChunkSize = 2 << 20; c.MaxConcurrentReads = 16 })
	ctx := context.Background()
	const size = 64 << 20
	if _, err := s.Put(ctx, "ws/a/big", &patternReader{total: size}, objectstore.PutOptions{}); err != nil {
		t.Fatal(err)
	}

	probe := func(n int) []time.Duration {
		var out []time.Duration
		for i := 0; i < n; i++ {
			st := time.Now()
			if _, err := ctrl.Exec(`INSERT INTO load_probe (v) VALUES ($1)`, "x"); err != nil {
				t.Fatal(err)
			}
			var c int
			if err := ctrl.QueryRow(`SELECT count(*) FROM load_probe WHERE id > $1`, i).Scan(&c); err != nil {
				t.Fatal(err)
			}
			out = append(out, time.Since(st))
			time.Sleep(5 * time.Millisecond)
		}
		return out
	}
	p95 := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[int(float64(len(d))*0.95)-1]
	}
	base := p95(probe(200))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readErr error
	var emu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rc, _, err := s.Get(ctx, "ws/a/big", nil)
				if err != nil {
					emu.Lock()
					readErr = err
					emu.Unlock()
					return
				}
				_, _ = io.Copy(io.Discard, rc)
				_ = rc.Close()
			}
		}()
	}
	time.Sleep(500 * time.Millisecond) // let the downloads ramp up
	loaded := p95(probe(200))
	close(stop)
	wg.Wait()
	if readErr != nil {
		t.Fatal(readErr)
	}
	t.Logf("CRUD probe p95: baseline %v, under 16 concurrent 64 MiB downloads %v", base, loaded)
	budget := 100 * time.Millisecond
	if loaded > budget && loaded > 5*base {
		t.Fatalf("CRUD p95 %v under load (baseline %v) exceeds the %v budget", loaded, base, budget)
	}
}
