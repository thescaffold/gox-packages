// Package objectstore is the one interface everything that stores bytes goes
// through (TRD §6.10, PLAN M1-05): artifacts, exports, evidence, shared
// prototype files, uploads. Drivers (`fs` here, `postgres` in M1-05a, `s3`
// later) are interchangeable behind it, and ONE driver-agnostic contract suite
// (package storetest) holds every driver to the same behaviour, so nothing
// driver-specific can leak into a caller.
//
// It is distinct from the legacy FilesService in this module, which is an HTTP
// client for the `blobs` server app and is untouched.
//
// Contract highlights:
//
//   - A Put is atomic: a reader sees the previous object or the complete new
//     one, never a partial one; a Put whose reader fails, or whose checksum
//     does not match, leaves nothing behind.
//   - Every object carries its byte size and SHA-256, computed while it is
//     written (never trusting the caller).
//   - Keys are validated by every driver (see ValidateKey); ForWorkspace
//     additionally confines a store to one tenant's key prefix, so a
//     cross-tenant read or overwrite is impossible on every backend.
//   - Nothing is ever publicly readable; Presign returns a time-limited,
//     signed download URL.
package objectstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// DefaultMaxObjectSize is the per-object cap drivers apply unless configured
// otherwise (TRD §6.10: 100 MiB for the Postgres backend).
const DefaultMaxObjectSize int64 = 100 << 20

// Errors are sentinels; drivers wrap them, callers use errors.Is.
var (
	ErrNotFound            = errors.New("objectstore: object not found")
	ErrExists              = errors.New("objectstore: object already exists")
	ErrInvalidKey          = errors.New("objectstore: invalid key")
	ErrTooLarge            = errors.New("objectstore: object exceeds the size limit of this storage backend; larger objects need the S3 backend (BLOBS_BACKEND=s3)")
	ErrChecksumMismatch    = errors.New("objectstore: content does not match the expected SHA-256")
	ErrRangeNotSatisfiable = errors.New("objectstore: range not satisfiable")
	ErrUploadNotFound      = errors.New("objectstore: multipart upload not found")
	ErrInvalidPart         = errors.New("objectstore: invalid or missing multipart part")
	ErrForbidden           = errors.New("objectstore: key is outside the allowed prefix")
	ErrLocked              = errors.New("objectstore: object is locked (immutable evidence) and cannot be changed or deleted")
	ErrQuotaExceeded       = errors.New("objectstore: workspace storage quota exceeded")
)

// ObjectInfo describes a stored object.
type ObjectInfo struct {
	Key       string
	Size      int64
	SHA256    string // lowercase hex, computed by the store
	MediaType string
	CreatedAt time.Time
	// Metadata is caller-supplied, stored verbatim (keys lower-cased).
	Metadata map[string]string
	Locked   bool
}

// PutOptions configure a write.
type PutOptions struct {
	MediaType string
	Metadata  map[string]string
	// ExpectedSHA256, when set, must equal the content's hash or the write is
	// rejected with ErrChecksumMismatch and nothing is stored.
	ExpectedSHA256 string
	// IfNotExists makes the write fail with ErrExists rather than overwrite.
	IfNotExists bool
	// Locked marks the object immutable once committed (evidence-class
	// objects, TRD §6.10): Put over it, Copy onto it and Delete of it fail
	// with ErrLocked. Drivers add their own enforcement below the API where
	// they can (the Postgres driver uses database triggers).
	Locked bool
	// ExpiresAt, when non-zero, lets a retention sweep remove the object
	// (ephemeral classes: exports, sandbox diagnostics).
	ExpiresAt time.Time
}

// Range selects bytes [Start, End] inclusive. Use Suffix for "the last N
// bytes". A nil *Range means the whole object.
type Range struct {
	Start, End int64
	// OpenEnd means "from Start to the end" (End is ignored).
	OpenEnd bool
	// Suffix, when > 0, means the last Suffix bytes (Start/End ignored).
	Suffix int64
}

// ListOptions page through keys in lexicographic order.
type ListOptions struct {
	// After is the last key of the previous page (exclusive); "" starts at the beginning.
	After string
	// Limit caps the page; <= 0 means 1000.
	Limit int
}

// ListPage is one page of a listing.
type ListPage struct {
	Objects []ObjectInfo
	// Next is the After value for the following page; "" when there is none.
	Next string
}

// PresignOptions configure a signed download URL.
type PresignOptions struct {
	// TTL is how long the URL works; drivers clamp it to 5–15 minutes by
	// default (TRD §6.10), 0 meaning the default.
	TTL time.Duration
	// Filename is offered to the browser as the download name.
	Filename string
	// SingleUse asks the serving route to honour the URL once.
	SingleUse bool
}

// UploadID identifies an in-progress multipart upload.
type UploadID string

// PartInfo identifies one uploaded part.
type PartInfo struct {
	Number int // 1-based
	Size   int64
	SHA256 string
}

// ObjectStore is the driver interface.
type ObjectStore interface {
	// Put streams r into key, hashing as it goes.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (ObjectInfo, error)
	// Get opens key (optionally a byte range). The returned info describes the
	// WHOLE object. The caller must Close the reader.
	Get(ctx context.Context, key string, rng *Range) (io.ReadCloser, ObjectInfo, error)
	Head(ctx context.Context, key string) (ObjectInfo, error)
	// Delete is idempotent: deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string, opts ListOptions) (ListPage, error)
	// Copy duplicates src to dst atomically (overwriting dst).
	Copy(ctx context.Context, src, dst string) (ObjectInfo, error)
	// Presign returns a signed, expiring download URL for key.
	Presign(ctx context.Context, key string, opts PresignOptions) (string, error)

	// Multipart: parts may arrive in any order and be retried; the object
	// becomes visible only at CompleteMultipart.
	BeginMultipart(ctx context.Context, key string, opts PutOptions) (UploadID, error)
	UploadPart(ctx context.Context, id UploadID, number int, r io.Reader) (PartInfo, error)
	CompleteMultipart(ctx context.Context, id UploadID, parts []PartInfo) (ObjectInfo, error)
	AbortMultipart(ctx context.Context, id UploadID) error
}
