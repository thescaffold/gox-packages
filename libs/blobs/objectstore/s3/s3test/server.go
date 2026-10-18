// Package s3test is a small in-memory S3 server for testing code that talks to S3
// (path-style, one bucket). It verifies AWS Signature V4 — header and query
// (presigned) forms — with its own implementation, and covers what the s3 driver
// uses: objects, conditional writes, ranges, server-side copy, listing, and
// multipart uploads with part checksums.
//
// It is not S3: it does not enforce the 5 MiB minimum part size, versioning,
// ACLs or Object Lock.
package s3test

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// blob is bytes kept in a temp file, so a large object does not sit on the Go
// heap (the shared contract suite measures the driver's memory use).
type blob struct {
	path string
	size int64
	etag string
}

type object struct {
	data    blob
	headers http.Header // Content-Type and x-amz-meta-*
	mod     time.Time
}

type part struct {
	data blob
	sha  string // base64
}

type upload struct {
	key     string
	headers http.Header
	parts   map[int]part
}

// Server is the fake. Create with New; it listens on a local port.
type Server struct {
	*httptest.Server
	Bucket, AccessKey, SecretKey, Region string

	dir     string
	mu      sync.Mutex
	objects map[string]*object
	uploads map[string]*upload
	nextID  int
	// Now lets a test move the server's clock (presign expiry).
	Now func() time.Time
}

// New starts a server for one bucket.
func New(bucket, accessKey, secretKey, region string) *Server {
	dir, err := os.MkdirTemp("", "s3test-")
	if err != nil {
		panic(err)
	}
	s := &Server{dir: dir, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey, Region: region,
		objects: map[string]*object{}, uploads: map[string]*upload{}, Now: time.Now}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the server and removes its files.
func (s *Server) Close() {
	s.Server.Close()
	_ = os.RemoveAll(s.dir)
}

func (s *Server) newBlob(chunks ...io.Reader) blob {
	f, err := os.CreateTemp(s.dir, "b-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	h := md5.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.MultiReader(chunks...))
	if err != nil {
		panic(err)
	}
	return blob{path: f.Name(), size: n, etag: `"` + hex.EncodeToString(h.Sum(nil)) + `"`}
}

func (b blob) open() *os.File {
	f, err := os.Open(b.path)
	if err != nil {
		panic(err)
	}
	return f
}

// Count is the number of stored objects.
func (s *Server) Count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.objects) }

// OpenUploads is the number of multipart uploads neither completed nor aborted.
func (s *Server) OpenUploads() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.uploads) }

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>%s</Message></Error>", code, msg)
}

// --- signature verification (written from the AWS spec, independent of the driver) ---

func enc(s string, slash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' || (c == '/' && slash) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func mac(k []byte, d string) []byte {
	m := hmac.New(sha256.New, k)
	m.Write([]byte(d))
	return m.Sum(nil)
}

func (s *Server) verify(r *http.Request, query url.Values) (payload string, ok bool) {
	var cred, signedList, sig, amzDate, expires string
	presigned := query.Get("X-Amz-Signature") != ""
	if presigned {
		cred, signedList, sig, amzDate, expires = query.Get("X-Amz-Credential"), query.Get("X-Amz-SignedHeaders"), query.Get("X-Amz-Signature"), query.Get("X-Amz-Date"), query.Get("X-Amz-Expires")
		payload = "UNSIGNED-PAYLOAD"
	} else {
		a := r.Header.Get("Authorization")
		if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 ") {
			return "", false
		}
		for _, f := range strings.Split(strings.TrimPrefix(a, "AWS4-HMAC-SHA256 "), ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(f), "=")
			switch k {
			case "Credential":
				cred = v
			case "SignedHeaders":
				signedList = v
			case "Signature":
				sig = v
			}
		}
		amzDate, payload = r.Header.Get("x-amz-date"), r.Header.Get("x-amz-content-sha256")
	}
	cp := strings.Split(cred, "/")
	if len(cp) != 5 || cp[0] != s.AccessKey || cp[2] != s.Region || cp[3] != "s3" {
		return "", false
	}
	t, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil || amzDate[:8] != cp[1] {
		return "", false
	}
	if presigned {
		sec, _ := strconv.Atoi(expires)
		if sec <= 0 || sec > 7*24*3600 || s.Now().After(t.Add(time.Duration(sec)*time.Second)) {
			return "", false
		}
	} else if d := s.Now().Sub(t); d > 15*time.Minute || d < -15*time.Minute {
		return "", false
	}
	// canonical query: every parameter but the signature itself
	var qp []string
	for k, vs := range query {
		if k == "X-Amz-Signature" {
			continue
		}
		for _, v := range vs {
			qp = append(qp, enc(k, false)+"="+enc(v, false))
		}
	}
	sort.Strings(qp)
	var hb strings.Builder
	for _, n := range strings.Split(signedList, ";") {
		v := strings.Join(strings.Fields(strings.Join(r.Header.Values(n), ",")), " ")
		if n == "host" {
			v = r.Host
		}
		hb.WriteString(n + ":" + v + "\n")
	}
	path, _ := url.PathUnescape(r.URL.EscapedPath())
	canon := strings.Join([]string{r.Method, enc(path, true), strings.Join(qp, "&"), hb.String(), signedList, payload}, "\n")
	h := sha256.Sum256([]byte(canon))
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + strings.Join(cp[1:], "/") + "\n" + hex.EncodeToString(h[:])
	k := mac(mac(mac(mac([]byte("AWS4"+s.SecretKey), cp[1]), cp[2]), "s3"), "aws4_request")
	return payload, hmac.Equal([]byte(hex.EncodeToString(mac(k, sts))), []byte(sig))
}

// --- handler --------------------------------------------------------------

func keepHeaders(h http.Header) http.Header {
	out := http.Header{}
	for k, v := range h {
		l := strings.ToLower(k)
		if strings.HasPrefix(l, "x-amz-meta-") || l == "content-type" {
			out[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
		}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	payload, ok := s.verify(r, q)
	if !ok {
		fail(w, 403, "SignatureDoesNotMatch", "the request signature does not match")
		return
	}
	var body []byte
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		body, _ = io.ReadAll(r.Body)
		if payload != "UNSIGNED-PAYLOAD" {
			h := sha256.Sum256(body)
			if hex.EncodeToString(h[:]) != payload {
				fail(w, 400, "XAmzContentSHA256Mismatch", "the provided x-amz-content-sha256 does not match what was computed")
				return
			}
		}
	}
	path, _ := url.PathUnescape(r.URL.EscapedPath())
	prefix := "/" + s.Bucket
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		fail(w, 404, "NoSuchBucket", "no such bucket")
		return
	}
	key := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	s.mu.Lock()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			s.mu.Unlock()
		}
	}
	defer unlock()

	if key == "" {
		if r.Method == http.MethodGet && q.Get("list-type") == "2" {
			s.list(w, q)
			return
		}
		fail(w, 405, "MethodNotAllowed", "unsupported bucket operation")
		return
	}
	upID := q.Get("uploadId")
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		s.nextID++
		id := fmt.Sprintf("up-%d-%d", s.nextID, s.Now().UnixNano())
		s.uploads[id] = &upload{key: key, headers: keepHeaders(r.Header), parts: map[int]part{}}
		fmt.Fprintf(w, "<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>", id)
	case r.Method == http.MethodPut && upID != "":
		u := s.uploads[upID]
		if u == nil || u.key != key {
			fail(w, 404, "NoSuchUpload", "the upload does not exist")
			return
		}
		n, _ := strconv.Atoi(q.Get("partNumber"))
		if n < 1 || n > 10000 {
			fail(w, 400, "InvalidArgument", "part number must be 1-10000")
			return
		}
		h := sha256.Sum256(body)
		got := base64.StdEncoding.EncodeToString(h[:])
		if c := r.Header.Get("x-amz-checksum-sha256"); c != "" && c != got {
			fail(w, 400, "BadDigest", "the checksum does not match")
			return
		}
		u.parts[n] = part{data: s.newBlob(bytes.NewReader(body)), sha: got}
		w.Header().Set("ETag", u.parts[n].data.etag)
	case r.Method == http.MethodGet && upID != "":
		u := s.uploads[upID]
		if u == nil || u.key != key {
			fail(w, 404, "NoSuchUpload", "the upload does not exist")
			return
		}
		marker, _ := strconv.Atoi(q.Get("part-number-marker"))
		max, _ := strconv.Atoi(q.Get("max-parts"))
		if max <= 0 || max > 1000 {
			max = 1000
		}
		var nums []int
		for n := range u.parts {
			if n > marker {
				nums = append(nums, n)
			}
		}
		sort.Ints(nums)
		trunc := len(nums) > max
		if trunc {
			nums = nums[:max]
		}
		var b strings.Builder
		fmt.Fprintf(&b, "<ListPartsResult><IsTruncated>%v</IsTruncated>", trunc)
		if trunc {
			fmt.Fprintf(&b, "<NextPartNumberMarker>%d</NextPartNumberMarker>", nums[len(nums)-1])
		}
		for _, n := range nums {
			p := u.parts[n]
			fmt.Fprintf(&b, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag><Size>%d</Size><ChecksumSHA256>%s</ChecksumSHA256></Part>", n, p.data.etag, p.data.size, p.sha)
		}
		b.WriteString("</ListPartsResult>")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(b.String()))
	case r.Method == http.MethodDelete && upID != "":
		delete(s.uploads, upID) // abort is idempotent in this fake
		w.WriteHeader(204)
	case r.Method == http.MethodPost && upID != "":
		s.completeUpload(w, r, key, upID, body)
	case r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "":
		s.copy(w, r, key)
	case r.Method == http.MethodPut:
		if r.Header.Get("If-None-Match") == "*" && s.objects[key] != nil {
			fail(w, 412, "PreconditionFailed", "the object already exists")
			return
		}
		b := s.newBlob(bytes.NewReader(body))
		s.objects[key] = &object{data: b, headers: keepHeaders(r.Header), mod: s.Now()}
		w.Header().Set("ETag", b.etag)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		o := s.objects[key]
		if o == nil {
			if r.Method == http.MethodHead {
				w.WriteHeader(404)
				return
			}
			fail(w, 404, "NoSuchKey", "the key does not exist")
			return
		}
		for k, v := range o.headers {
			w.Header()[k] = v
		}
		w.Header().Set("Last-Modified", o.mod.UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", o.data.etag)
		size := o.data.size
		from, to := int64(0), size-1
		if rg := r.Header.Get("Range"); rg != "" {
			if _, err := fmt.Sscanf(rg, "bytes=%d-%d", &from, &to); err != nil || from < 0 || from >= size || to < from {
				fail(w, 416, "InvalidRange", "the requested range is not satisfiable")
				return
			}
			if to >= size {
				to = size - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, size))
			w.Header().Set("Content-Length", strconv.FormatInt(to-from+1, 10))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		}
		if r.Method == http.MethodGet && size > 0 {
			unlock() // streaming a body must not block other requests
			f := o.data.open()
			defer func() { _ = f.Close() }()
			_, _ = io.Copy(w, io.NewSectionReader(f, from, to-from+1))
		}
	case r.Method == http.MethodDelete:
		delete(s.objects, key)
		w.WriteHeader(204)
	default:
		fail(w, 405, "MethodNotAllowed", "unsupported operation")
	}
}

func (s *Server) copy(w http.ResponseWriter, r *http.Request, key string) {
	src, _ := url.PathUnescape(r.Header.Get("x-amz-copy-source"))
	src = strings.TrimPrefix(strings.TrimPrefix(src, "/"+s.Bucket), "/")
	o := s.objects[src]
	if o == nil {
		fail(w, 404, "NoSuchKey", "the source key does not exist")
		return
	}
	hdr := o.headers
	if strings.EqualFold(r.Header.Get("x-amz-metadata-directive"), "REPLACE") {
		hdr = keepHeaders(r.Header)
	} else if src == key {
		fail(w, 400, "InvalidRequest", "copying an object onto itself needs a metadata change")
		return
	}
	f := o.data.open()
	defer func() { _ = f.Close() }()
	s.objects[key] = &object{data: s.newBlob(f), headers: hdr, mod: s.Now()}
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, "<CopyObjectResult><ETag>%s</ETag></CopyObjectResult>", o.data.etag)
}

func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request, key, id string, body []byte) {
	u := s.uploads[id]
	if u == nil || u.key != key {
		fail(w, 404, "NoSuchUpload", "the upload does not exist")
		return
	}
	var req struct {
		Parts []struct {
			N    int    `xml:"PartNumber"`
			ETag string `xml:"ETag"`
			SHA  string `xml:"ChecksumSHA256"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) == 0 {
		fail(w, 400, "MalformedXML", "the XML is not well-formed")
		return
	}
	if r.Header.Get("If-None-Match") == "*" && s.objects[key] != nil {
		fail(w, 412, "PreconditionFailed", "the object already exists")
		return
	}
	var readers []io.Reader
	last := 0
	for _, p := range req.Parts {
		if p.N <= last {
			fail(w, 400, "InvalidPartOrder", "parts must be in ascending order")
			return
		}
		last = p.N
		have, ok := u.parts[p.N]
		if !ok || have.data.etag != p.ETag || have.sha != p.SHA {
			fail(w, 400, "InvalidPart", "one or more parts could not be found or did not match")
			return
		}
		f := have.data.open()
		defer func() { _ = f.Close() }()
		readers = append(readers, f)
	}
	s.objects[key] = &object{data: s.newBlob(readers...), headers: u.headers, mod: s.Now()}
	delete(s.uploads, id)
	w.Header().Set("Content-Type", "application/xml")
	fmt.Fprintf(w, "<CompleteMultipartUploadResult><Key>%s</Key></CompleteMultipartUploadResult>", key)
}

func (s *Server) list(w http.ResponseWriter, q url.Values) {
	prefix, after := q.Get("prefix"), q.Get("start-after")
	max, _ := strconv.Atoi(q.Get("max-keys"))
	if max <= 0 || max > 1000 {
		max = 1000
	}
	var keys []string
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	trunc := len(keys) > max
	if trunc {
		keys = keys[:max]
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "<ListBucketResult><IsTruncated>%v</IsTruncated>", trunc)
	for _, k := range keys {
		o := s.objects[k]
		fmt.Fprintf(&b, "<Contents><Key>%s</Key><Size>%d</Size><LastModified>%s</LastModified></Contents>", xmlText(k), o.data.size, o.mod.UTC().Format(time.RFC3339))
	}
	b.WriteString("</ListBucketResult>")
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(b.Bytes())
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
