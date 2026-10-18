// Package s3 is the objectstore driver for S3-compatible storage (AWS S3,
// MinIO, Cloudflare R2, Ceph…). It implements the whole objectstore.ObjectStore
// contract and holds to the same shared suite as the fs and Postgres drivers,
// and adds PresignPut: a native, expiring URL a browser can PUT to directly, so
// large uploads never pass through the application server.
//
// It speaks the S3 REST API itself (AWS Signature V4, see sign.go) rather than
// pulling in the AWS SDK.
//
// How the contract maps onto S3:
//
//   - Put streams through a bounded buffer: an object that fits one part is a
//     single PUT signed with its real SHA-256; a larger one is a multipart
//     upload, aborted if the reader fails, the size limit is exceeded or the
//     checksum is wrong, so a failed Put leaves nothing behind.
//   - ObjectInfo (SHA-256, locked flag, creation time, caller metadata) lives in
//     the object's own user metadata; List returns only key, size and time.
//   - Locked is enforced by this driver (a lock check before every write, copy
//     and delete). It is not S3 Object Lock: for immutability that survives a
//     bypassed driver, also enable Object Lock on the bucket.
//   - IfNotExists uses the conditional write If-None-Match: *.
//   - Presign returns the same Origine-signed /api/blobs/dl/ URL as every other
//     driver (the host serves the bytes, so it can authorise, audit and
//     single-use them); PresignPut is the native direct-to-bucket URL.
//   - An object copied or completed through multipart is re-stamped with its
//     whole-object SHA-256 by a server-side copy onto itself, so objects are
//     limited to 5 GiB (the single-copy limit) by default.
package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thescaffold/gox-packages/libs/blobs/objectstore"
)

// Config configures a Store.
type Config struct {
	// Endpoint is the service URL, e.g. "http://localhost:9000" (MinIO). Empty
	// means AWS S3 in Region.
	Endpoint string
	Region   string
	Bucket   string

	AccessKeyID, SecretAccessKey, SessionToken string

	// PathStyle addresses the bucket as /<bucket>/<key> (what MinIO and most
	// self-hosted services need). It is on whenever Endpoint is set.
	PathStyle bool
	// Prefix puts every key under a prefix inside the bucket.
	Prefix string

	// PresignSecret signs Presign (download) URLs; at least 16 bytes.
	PresignSecret []byte
	// MaxObjectSize caps one object; default 5 GiB, which is also the most S3
	// can copy server-side in one request.
	MaxObjectSize int64
	// PartSize is the multipart part size and the largest object written with a
	// single PUT; default 8 MiB. S3 itself requires at least 5 MiB per part.
	PartSize int64

	HTTPClient *http.Client
	Now        func() time.Time
}

// Store is an S3-backed objectstore.ObjectStore.
type Store struct {
	cfg    Config
	creds  credentials
	client *http.Client
	scheme string
	host   string // host used in URLs (bucket-prefixed for virtual-hosted style)
	path   string // path prefix before the key ("/<bucket>/" or "/")
	prefix string
}

var _ objectstore.ObjectStore = (*Store)(nil)

const (
	defaultMax      = int64(5) << 30
	defaultPartSize = int64(8) << 20
	maxParts        = 10_000
)

// New validates cfg and returns a Store. It makes no network calls.
func New(cfg Config) (*Store, error) {
	switch {
	case cfg.Bucket == "":
		return nil, errors.New("s3: bucket is required")
	case cfg.AccessKeyID == "" || cfg.SecretAccessKey == "":
		return nil, errors.New("s3: access key id and secret are required")
	case len(cfg.PresignSecret) < 16:
		return nil, errors.New("s3: presign secret must be at least 16 bytes")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.MaxObjectSize <= 0 {
		cfg.MaxObjectSize = defaultMax
	}
	if cfg.PartSize <= 0 {
		cfg.PartSize = defaultPartSize
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 0}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Store{cfg: cfg, client: cfg.HTTPClient, creds: credentials{cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken}}
	if p := strings.Trim(cfg.Prefix, "/"); p != "" {
		s.prefix = p + "/"
	}
	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("s3: bad endpoint %q", cfg.Endpoint)
		}
		s.scheme, s.host, s.path = u.Scheme, u.Host, "/"+cfg.Bucket+"/"
		return s, nil
	}
	s.scheme = "https"
	if cfg.PathStyle {
		s.host, s.path = "s3."+cfg.Region+".amazonaws.com", "/"+cfg.Bucket+"/"
	} else {
		s.host, s.path = cfg.Bucket+".s3."+cfg.Region+".amazonaws.com", "/"
	}
	return s, nil
}

func (s *Store) objectURL(key string, q url.Values) *url.URL {
	p := s.path + s.prefix + key
	u := &url.URL{Scheme: s.scheme, Host: s.host, Path: p, RawPath: uriEncode(p, true)}
	if q != nil {
		u.RawQuery = canonicalQuery(q)
	}
	return u
}

func (s *Store) bucketURL(q url.Values) *url.URL {
	p := s.path
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	u := &url.URL{Scheme: s.scheme, Host: s.host, Path: p, RawPath: uriEncode(p, true)}
	if q != nil {
		u.RawQuery = canonicalQuery(q)
	}
	return u
}

// --- requests -------------------------------------------------------------

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("s3: %s (%d): %s", e.Code, e.Status, e.Message)
}

func readError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var x struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	_ = xml.Unmarshal(b, &x)
	if x.Code == "" {
		switch resp.StatusCode {
		case 404:
			x.Code = "NotFound"
		case 412:
			x.Code = "PreconditionFailed"
		case 416:
			x.Code = "InvalidRange"
		default:
			x.Code = http.StatusText(resp.StatusCode)
		}
	}
	return &apiError{Status: resp.StatusCode, Code: x.Code, Message: x.Message}
}

// do sends a signed request. body may be nil; payload is its SHA-256 (or
// UNSIGNED-PAYLOAD). The caller closes the response body on success.
func (s *Store) do(ctx context.Context, method string, u *url.URL, hdr http.Header, body []byte, payload string) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	if payload == "" {
		payload = emptySHA256
	}
	signHeaders(req, s.creds, s.cfg.Region, payload, s.cfg.Now())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, readError(resp)
	}
	return resp, nil
}

func asAPI(err error) *apiError {
	var a *apiError
	if errors.As(err, &a) {
		return a
	}
	return nil
}

// mapErr turns S3 error codes into the objectstore sentinels.
func mapErr(err error) error {
	a := asAPI(err)
	if a == nil {
		return err
	}
	switch {
	case a.Code == "NoSuchKey" || (a.Code == "NotFound" && a.Status == 404):
		return objectstore.ErrNotFound
	case a.Code == "NoSuchUpload":
		return objectstore.ErrUploadNotFound
	case a.Code == "PreconditionFailed" || a.Status == 412:
		return objectstore.ErrExists
	case a.Code == "InvalidRange" || a.Status == 416:
		return objectstore.ErrRangeNotSatisfiable
	case a.Code == "EntityTooLarge":
		return objectstore.ErrTooLarge
	case a.Code == "InvalidPart" || a.Code == "InvalidPartOrder":
		return objectstore.ErrInvalidPart
	}
	return err
}

// --- metadata -------------------------------------------------------------

const (
	hSHA     = "x-amz-meta-os-sha256"
	hLocked  = "x-amz-meta-os-locked"
	hCreated = "x-amz-meta-os-created"
	hExpires = "x-amz-meta-os-expires"
	hUser    = "x-amz-meta-u-"
	hUserHex = "x-amz-meta-h-"
)

func plainMetaKey(k string) bool {
	if k == "" || len(k) > 100 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_') {
			return false
		}
	}
	return true
}

// metaHeaders is the header set that records an object's ObjectInfo.
func metaHeaders(h http.Header, media string, meta map[string]string, sha string, locked bool, created, expires time.Time) {
	if media != "" {
		h.Set("Content-Type", media)
	}
	if sha != "" {
		h.Set(hSHA, sha)
	}
	if locked {
		h.Set(hLocked, "1")
	}
	h.Set(hCreated, created.UTC().Format(time.RFC3339Nano))
	if !expires.IsZero() {
		h.Set(hExpires, expires.UTC().Format(time.RFC3339Nano))
	}
	for k, v := range meta {
		k = strings.ToLower(k)
		name := hUser + k
		if !plainMetaKey(k) {
			name = hUserHex + hex.EncodeToString([]byte(k))
		}
		h.Set(name, url.QueryEscape(v))
	}
}

func infoFrom(key string, size int64, h http.Header) objectstore.ObjectInfo {
	info := objectstore.ObjectInfo{Key: key, Size: size, MediaType: h.Get("Content-Type"), Metadata: map[string]string{}}
	for name := range h {
		l := strings.ToLower(name)
		v := h.Get(name)
		switch {
		case l == hSHA:
			info.SHA256 = v
		case l == hLocked:
			info.Locked = v == "1"
		case l == hCreated:
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				info.CreatedAt = t
			}
		case strings.HasPrefix(l, hUser):
			info.Metadata[strings.TrimPrefix(l, hUser)], _ = url.QueryUnescape(v)
		case strings.HasPrefix(l, hUserHex):
			if raw, err := hex.DecodeString(strings.TrimPrefix(l, hUserHex)); err == nil {
				info.Metadata[string(raw)], _ = url.QueryUnescape(v)
			}
		}
	}
	if info.CreatedAt.IsZero() {
		if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
			info.CreatedAt = t
		}
	}
	if len(info.Metadata) == 0 {
		info.Metadata = nil
	}
	return info
}

// --- operations -----------------------------------------------------------

func (s *Store) check(ctx context.Context, key string) error {
	if err := objectstore.ValidateKey(key); err != nil {
		return err
	}
	return ctx.Err()
}

// Head returns an object's info.
func (s *Store) Head(ctx context.Context, key string) (objectstore.ObjectInfo, error) {
	if err := s.check(ctx, key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	resp, err := s.do(ctx, http.MethodHead, s.objectURL(key, nil), nil, nil, "")
	if err != nil {
		return objectstore.ObjectInfo{}, mapErr(err)
	}
	_ = resp.Body.Close()
	return infoFrom(key, resp.ContentLength, resp.Header), nil
}

// readerClose pairs a body with its connection.
type bodyReader struct{ io.ReadCloser }

// Get opens key, or a byte range of it.
func (s *Store) Get(ctx context.Context, key string, rng *objectstore.Range) (io.ReadCloser, objectstore.ObjectInfo, error) {
	if err := s.check(ctx, key); err != nil {
		return nil, objectstore.ObjectInfo{}, err
	}
	hdr := http.Header{}
	var whole *objectstore.ObjectInfo
	if rng != nil {
		head, err := s.Head(ctx, key)
		if err != nil {
			return nil, objectstore.ObjectInfo{}, err
		}
		start, length, err := objectstore.ResolveRange(rng, head.Size)
		if err != nil {
			return nil, objectstore.ObjectInfo{}, err
		}
		hdr.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+length-1))
		whole = &head
	}
	resp, err := s.do(ctx, http.MethodGet, s.objectURL(key, nil), hdr, nil, "")
	if err != nil {
		return nil, objectstore.ObjectInfo{}, mapErr(err)
	}
	if whole != nil {
		return bodyReader{resp.Body}, *whole, nil
	}
	return bodyReader{resp.Body}, infoFrom(key, resp.ContentLength, resp.Header), nil
}

// lockedErr reports ErrLocked when key exists and is locked.
func (s *Store) lockedErr(ctx context.Context, key string) error {
	h, err := s.Head(ctx, key)
	switch {
	case errors.Is(err, objectstore.ErrNotFound):
		return nil
	case err != nil:
		return err
	case h.Locked:
		return objectstore.ErrLocked
	}
	return nil
}

// Delete removes key; a missing key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.check(ctx, key); err != nil {
		return err
	}
	if err := s.lockedErr(ctx, key); err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, s.objectURL(key, nil), nil, nil, "")
	if err != nil {
		if errors.Is(mapErr(err), objectstore.ErrNotFound) {
			return nil
		}
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// Put stores r under key.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, opts objectstore.PutOptions) (objectstore.ObjectInfo, error) {
	if err := s.check(ctx, key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if !opts.IfNotExists {
		if err := s.lockedErr(ctx, key); err != nil {
			return objectstore.ObjectInfo{}, err
		}
	}
	created := s.cfg.Now()
	sum := sha256.New()
	var total int64
	var upload string // multipart upload id, once started
	var parts []objectstore.PartInfo
	abort := func() {
		if upload != "" {
			_ = s.abort(context.WithoutCancel(ctx), key, upload)
		}
	}
	buf := make([]byte, s.cfg.PartSize)
	for {
		n, rerr := io.ReadFull(r, buf)
		last := rerr == io.EOF || rerr == io.ErrUnexpectedEOF
		if rerr != nil && !last {
			abort()
			return objectstore.ObjectInfo{}, rerr
		}
		total += int64(n)
		if total > s.cfg.MaxObjectSize {
			abort()
			return objectstore.ObjectInfo{}, objectstore.ErrTooLarge
		}
		sum.Write(buf[:n])
		chunk := buf[:n]

		if last && upload == "" {
			// The whole object fits one part: a single PUT, signed with its real hash.
			sha := hex.EncodeToString(sum.Sum(nil))
			if opts.ExpectedSHA256 != "" && !strings.EqualFold(opts.ExpectedSHA256, sha) {
				return objectstore.ObjectInfo{}, objectstore.ErrChecksumMismatch
			}
			h := http.Header{}
			metaHeaders(h, opts.MediaType, opts.Metadata, sha, opts.Locked, created, opts.ExpiresAt)
			if opts.IfNotExists {
				h.Set("If-None-Match", "*")
			}
			resp, err := s.do(ctx, http.MethodPut, s.objectURL(key, nil), h, chunk, sha)
			if err != nil {
				return objectstore.ObjectInfo{}, mapErr(err)
			}
			_ = resp.Body.Close()
			return s.infoOf(key, total, sha, opts, created), nil
		}

		if upload == "" {
			id, err := s.createMultipart(ctx, key, opts, created)
			if err != nil {
				return objectstore.ObjectInfo{}, err
			}
			upload = id
		}
		if n > 0 {
			pi, err := s.putPart(ctx, key, upload, len(parts)+1, chunk)
			if err != nil {
				abort()
				return objectstore.ObjectInfo{}, err
			}
			parts = append(parts, pi)
		}
		if last {
			break
		}
	}
	sha := hex.EncodeToString(sum.Sum(nil))
	if opts.ExpectedSHA256 != "" && !strings.EqualFold(opts.ExpectedSHA256, sha) {
		abort()
		return objectstore.ObjectInfo{}, objectstore.ErrChecksumMismatch
	}
	if err := s.complete(ctx, key, upload, parts, opts.IfNotExists); err != nil {
		abort()
		return objectstore.ObjectInfo{}, err
	}
	if err := s.stamp(ctx, key, sha); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return s.infoOf(key, total, sha, opts, created), nil
}

func (s *Store) infoOf(key string, size int64, sha string, o objectstore.PutOptions, created time.Time) objectstore.ObjectInfo {
	md := map[string]string(nil)
	if len(o.Metadata) > 0 {
		md = map[string]string{}
		for k, v := range o.Metadata {
			md[strings.ToLower(k)] = v
		}
	}
	return objectstore.ObjectInfo{Key: key, Size: size, SHA256: sha, MediaType: o.MediaType, CreatedAt: created, Metadata: md, Locked: o.Locked}
}

// stamp records the whole-object SHA-256 on an object assembled from parts, by a
// server-side copy of the object onto itself with replaced metadata.
func (s *Store) stamp(ctx context.Context, key, sha string) error {
	resp, err := s.do(ctx, http.MethodHead, s.objectURL(key, nil), nil, nil, "")
	if err != nil {
		return mapErr(err)
	}
	_ = resp.Body.Close()
	h := http.Header{}
	for k, v := range resp.Header {
		l := strings.ToLower(k)
		if strings.HasPrefix(l, "x-amz-meta-") || l == "content-type" {
			h[k] = v
		}
	}
	h.Set(hSHA, sha)
	return s.copyOnto(ctx, key, key, h)
}

func (s *Store) copyOnto(ctx context.Context, src, dst string, h http.Header) error {
	h.Set("x-amz-copy-source", uriEncode("/"+s.cfg.Bucket+"/"+s.prefix+src, true))
	h.Set("x-amz-metadata-directive", "REPLACE")
	resp, err := s.do(ctx, http.MethodPut, s.objectURL(dst, nil), h, nil, "")
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkBodyError(resp.Body) // a copy can fail after a 200
}

// checkBodyError turns a 200 response that carries an <Error> into an error: S3
// reports some failures that way once it has started to answer.
func checkBodyError(r io.Reader) error {
	b, _ := io.ReadAll(io.LimitReader(r, 1<<16))
	if !bytes.Contains(b, []byte("<Error>")) {
		return nil
	}
	var x struct{ Code, Message string }
	_ = xml.Unmarshal(b, &x)
	return mapErr(&apiError{Status: 500, Code: x.Code, Message: x.Message})
}

// Copy duplicates src to dst.
func (s *Store) Copy(ctx context.Context, src, dst string) (objectstore.ObjectInfo, error) {
	if err := s.check(ctx, src); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if err := s.check(ctx, dst); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	head, err := s.Head(ctx, src)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if err := s.lockedErr(ctx, dst); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	created := s.cfg.Now()
	h := http.Header{}
	metaHeaders(h, head.MediaType, head.Metadata, head.SHA256, false, created, time.Time{})
	if err := s.copyOnto(ctx, src, dst, h); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	head.Key, head.Locked, head.CreatedAt = dst, false, created
	return head, nil
}

type listResult struct {
	IsTruncated bool `xml:"IsTruncated"`
	Contents    []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
}

// List returns keys under prefix in order. It reports key, size and time only;
// Head an object for its SHA-256 and metadata.
func (s *Store) List(ctx context.Context, prefix string, opts objectstore.ListOptions) (objectstore.ListPage, error) {
	if err := objectstore.ValidatePrefix(prefix); err != nil {
		return objectstore.ListPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return objectstore.ListPage{}, err
	}
	limit := opts.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	q := url.Values{"list-type": {"2"}, "prefix": {s.prefix + prefix}, "max-keys": {strconv.Itoa(limit)}}
	if opts.After != "" {
		q.Set("start-after", s.prefix+opts.After)
	}
	resp, err := s.do(ctx, http.MethodGet, s.bucketURL(q), nil, nil, "")
	if err != nil {
		return objectstore.ListPage{}, mapErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var lr listResult
	if err := xml.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return objectstore.ListPage{}, fmt.Errorf("s3: bad listing: %w", err)
	}
	page := objectstore.ListPage{}
	for _, c := range lr.Contents {
		page.Objects = append(page.Objects, objectstore.ObjectInfo{Key: strings.TrimPrefix(c.Key, s.prefix), Size: c.Size, CreatedAt: c.LastModified})
	}
	if lr.IsTruncated && len(page.Objects) > 0 {
		page.Next = page.Objects[len(page.Objects)-1].Key
	}
	return page, nil
}

// Presign returns an Origine-signed download URL (see the package comment).
func (s *Store) Presign(ctx context.Context, key string, opts objectstore.PresignOptions) (string, error) {
	if _, err := s.Head(ctx, key); err != nil {
		return "", err
	}
	return objectstore.SignDownload(key, opts, s.cfg.PresignSecret, s.cfg.Now())
}

// PresignPut returns a native S3 URL that accepts one PUT of the object, valid
// for ttl (clamped to 5–15 minutes). The upload must send exactly the given
// Content-Type. The object is not written with ObjectInfo metadata, so callers
// that need the SHA-256 or lock flag stamp it after the upload (Head, then Copy).
func (s *Store) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	if err := s.check(ctx, key); err != nil {
		return "", err
	}
	hdr := http.Header{}
	if contentType != "" {
		hdr.Set("Content-Type", contentType)
	}
	return presign(http.MethodPut, s.objectURL(key, nil), s.creds, s.cfg.Region, objectstore.ClampTTL(ttl), hdr, s.cfg.Now()), nil
}

// --- multipart ------------------------------------------------------------

// The public UploadID carries the key, so Complete/Abort need no server state:
// base64url(key) "." S3 upload id.
func encodeID(key, upload string) objectstore.UploadID {
	return objectstore.UploadID(base64.RawURLEncoding.EncodeToString([]byte(key)) + "." + upload)
}

func decodeID(id objectstore.UploadID) (key, upload string, err error) {
	a, b, ok := strings.Cut(string(id), ".")
	if !ok || a == "" || b == "" {
		return "", "", objectstore.ErrUploadNotFound
	}
	raw, derr := base64.RawURLEncoding.DecodeString(a)
	if derr != nil || objectstore.ValidateKey(string(raw)) != nil {
		return "", "", objectstore.ErrUploadNotFound
	}
	return string(raw), b, nil
}

func (s *Store) createMultipart(ctx context.Context, key string, o objectstore.PutOptions, created time.Time) (string, error) {
	h := http.Header{}
	metaHeaders(h, o.MediaType, o.Metadata, "", o.Locked, created, o.ExpiresAt)
	h.Set("x-amz-checksum-algorithm", "SHA256")
	resp, err := s.do(ctx, http.MethodPost, s.objectURL(key, url.Values{"uploads": {""}}), h, nil, "")
	if err != nil {
		return "", mapErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var x struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&x); err != nil || x.UploadID == "" {
		return "", errors.New("s3: could not start a multipart upload")
	}
	return x.UploadID, nil
}

func b64sha(hexsum string) string {
	raw, _ := hex.DecodeString(hexsum)
	return base64.StdEncoding.EncodeToString(raw)
}

func (s *Store) putPart(ctx context.Context, key, upload string, n int, data []byte) (objectstore.PartInfo, error) {
	h := sha256.Sum256(data)
	sha := hex.EncodeToString(h[:])
	hdr := http.Header{}
	hdr.Set("x-amz-checksum-sha256", b64sha(sha))
	resp, err := s.do(ctx, http.MethodPut, s.objectURL(key, url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {upload}}), hdr, data, sha)
	if err != nil {
		return objectstore.PartInfo{}, mapErr(err)
	}
	_ = resp.Body.Close()
	return objectstore.PartInfo{Number: n, Size: int64(len(data)), SHA256: sha}, nil
}

type partListing struct {
	IsTruncated bool `xml:"IsTruncated"`
	NextMarker  int  `xml:"NextPartNumberMarker"`
	Parts       []struct {
		Number int    `xml:"PartNumber"`
		ETag   string `xml:"ETag"`
		Size   int64  `xml:"Size"`
		SHA256 string `xml:"ChecksumSHA256"`
	} `xml:"Part"`
}

type remotePart struct {
	etag, sha string
	size      int64
}

func (s *Store) listParts(ctx context.Context, key, upload string) (map[int]remotePart, error) {
	out := map[int]remotePart{}
	marker := 0
	for {
		q := url.Values{"uploadId": {upload}, "max-parts": {"1000"}}
		if marker > 0 {
			q.Set("part-number-marker", strconv.Itoa(marker))
		}
		resp, err := s.do(ctx, http.MethodGet, s.objectURL(key, q), nil, nil, "")
		if err != nil {
			return nil, mapErr(err)
		}
		var pl partListing
		derr := xml.NewDecoder(resp.Body).Decode(&pl)
		_ = resp.Body.Close()
		if derr != nil {
			return nil, fmt.Errorf("s3: bad part listing: %w", derr)
		}
		for _, p := range pl.Parts {
			out[p.Number] = remotePart{etag: p.ETag, sha: p.SHA256, size: p.Size}
		}
		if !pl.IsTruncated {
			return out, nil
		}
		marker = pl.NextMarker
	}
}

func (s *Store) complete(ctx context.Context, key, upload string, parts []objectstore.PartInfo, ifNotExists bool) error {
	remote, err := s.listParts(ctx, key, upload)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("<CompleteMultipartUpload>")
	for _, p := range parts {
		rp, ok := remote[p.Number]
		if !ok {
			return objectstore.ErrInvalidPart
		}
		fmt.Fprintf(&b, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag><ChecksumSHA256>%s</ChecksumSHA256></Part>", p.Number, xmlEscape(rp.etag), rp.sha)
	}
	b.WriteString("</CompleteMultipartUpload>")
	h := http.Header{"Content-Type": {"application/xml"}}
	if ifNotExists {
		h.Set("If-None-Match", "*")
	}
	body := []byte(b.String())
	resp, err := s.do(ctx, http.MethodPost, s.objectURL(key, url.Values{"uploadId": {upload}}), h, body, sha256Hex(body))
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkBodyError(resp.Body)
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (s *Store) abort(ctx context.Context, key, upload string) error {
	resp, err := s.do(ctx, http.MethodDelete, s.objectURL(key, url.Values{"uploadId": {upload}}), nil, nil, "")
	if err != nil {
		if errors.Is(mapErr(err), objectstore.ErrUploadNotFound) {
			return nil
		}
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// BeginMultipart starts an upload; the object stays invisible until CompleteMultipart.
func (s *Store) BeginMultipart(ctx context.Context, key string, opts objectstore.PutOptions) (objectstore.UploadID, error) {
	if err := s.check(ctx, key); err != nil {
		return "", err
	}
	id, err := s.createMultipart(ctx, key, opts, s.cfg.Now())
	if err != nil {
		return "", err
	}
	return encodeID(key, id), nil
}

// UploadPart stores one part (1–10000); re-sending a number replaces it.
func (s *Store) UploadPart(ctx context.Context, id objectstore.UploadID, number int, r io.Reader) (objectstore.PartInfo, error) {
	key, upload, err := decodeID(id)
	if err != nil {
		return objectstore.PartInfo{}, err
	}
	if number < 1 || number > maxParts {
		return objectstore.PartInfo{}, fmt.Errorf("%w: part number %d", objectstore.ErrInvalidPart, number)
	}
	data, err := io.ReadAll(io.LimitReader(r, s.cfg.MaxObjectSize+1))
	if err != nil {
		return objectstore.PartInfo{}, err
	}
	if int64(len(data)) > s.cfg.MaxObjectSize {
		return objectstore.PartInfo{}, objectstore.ErrTooLarge
	}
	return s.putPart(ctx, key, upload, number, data)
}

// CompleteMultipart assembles the listed parts, in number order, into the object.
func (s *Store) CompleteMultipart(ctx context.Context, id objectstore.UploadID, parts []objectstore.PartInfo) (objectstore.ObjectInfo, error) {
	key, upload, err := decodeID(id)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if len(parts) == 0 {
		return objectstore.ObjectInfo{}, fmt.Errorf("%w: no parts", objectstore.ErrInvalidPart)
	}
	for i, p := range parts {
		if p.Number < 1 || p.Number > maxParts || (i > 0 && p.Number <= parts[i-1].Number) {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: parts must be listed once each in number order", objectstore.ErrInvalidPart)
		}
	}
	remote, err := s.listParts(ctx, key, upload)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	var total int64
	for _, p := range parts {
		rp, ok := remote[p.Number]
		if !ok || rp.size != p.Size || rp.sha != b64sha(p.SHA256) {
			return objectstore.ObjectInfo{}, fmt.Errorf("%w: part %d was not uploaded as listed", objectstore.ErrInvalidPart, p.Number)
		}
		total += rp.size
	}
	if total > s.cfg.MaxObjectSize {
		return objectstore.ObjectInfo{}, objectstore.ErrTooLarge
	}
	if err := s.lockedErr(ctx, key); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	if err := s.complete(ctx, key, upload, parts, false); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	// The object's SHA-256 is of the whole content: read it back once.
	rc, _, err := s.Get(ctx, key, nil)
	if err != nil {
		return objectstore.ObjectInfo{}, err
	}
	h := sha256.New()
	_, cerr := io.Copy(h, rc)
	_ = rc.Close()
	if cerr != nil {
		return objectstore.ObjectInfo{}, cerr
	}
	if err := s.stamp(ctx, key, hex.EncodeToString(h.Sum(nil))); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	return s.Head(ctx, key)
}

// AbortMultipart discards an upload; unknown or finished uploads are a no-op.
func (s *Store) AbortMultipart(ctx context.Context, id objectstore.UploadID) error {
	key, upload, err := decodeID(id)
	if err != nil {
		return nil
	}
	return s.abort(ctx, key, upload)
}
