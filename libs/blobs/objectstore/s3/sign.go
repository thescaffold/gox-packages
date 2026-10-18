package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4, the only part of the AWS SDK this driver needs.
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv4-create-signed-request.html

const (
	algorithm       = "AWS4-HMAC-SHA256"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	emptySHA256     = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type credentials struct {
	accessKey, secretKey, sessionToken string
}

func sha256Hex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func signingKey(secret, date, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, "s3")
	return hmacSHA256(k, "aws4_request")
}

// uriEncode percent-encodes everything but unreserved characters; '/' is kept
// when keepSlash is set (an object key's path separators).
func uriEncode(s string, keepSlash bool) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexd[c>>4])
			b.WriteByte(hexd[c&15])
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k, false)+"="+uriEncode(v, false))
		}
	}
	return strings.Join(parts, "&")
}

// canonicalHeaders returns the canonical header block and the signed-headers
// list for the named headers (lower-case names, trimmed single-spaced values).
func canonicalHeaders(h http.Header, host string, names []string) (block, signed string) {
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		var v string
		if n == "host" {
			v = host
		} else {
			v = strings.Join(h.Values(http.CanonicalHeaderKey(n)), ",")
		}
		b.WriteString(n + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	return b.String(), strings.Join(names, ";")
}

func scope(date, region string) string { return date + "/" + region + "/s3/aws4_request" }

// signedHeaderNames picks what to sign: host, content-type and range, and every
// x-amz-* header the request carries.
func signedHeaderNames(h http.Header) []string {
	names := []string{"host"}
	for k := range h {
		l := strings.ToLower(k)
		if strings.HasPrefix(l, "x-amz-") || l == "content-type" || l == "range" || l == "if-none-match" {
			names = append(names, l)
		}
	}
	return names
}

// signHeaders adds an Authorization header (and x-amz-date, x-amz-content-sha256,
// and the session token) to req.
func signHeaders(req *http.Request, c credentials, region, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if c.sessionToken != "" {
		req.Header.Set("x-amz-security-token", c.sessionToken)
	}
	host := req.URL.Host
	block, signed := canonicalHeaders(req.Header, host, signedHeaderNames(req.Header))
	canon := strings.Join([]string{
		req.Method, canonicalURI(req.URL), canonicalQuery(req.URL.Query()), block, signed, payloadHash,
	}, "\n")
	sts := strings.Join([]string{algorithm, amzDate, scope(date, region), sha256Hex([]byte(canon))}, "\n")
	sig := hex.EncodeToString(hmacSHA256(signingKey(c.secretKey, date, region), sts))
	req.Header.Set("Authorization", algorithm+" Credential="+c.accessKey+"/"+scope(date, region)+
		", SignedHeaders="+signed+", Signature="+sig)
}

// canonicalURI is the object path, encoded once (S3 does not double-encode).
func canonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	// EscapedPath may leave characters the signer must encode (it keeps "!*'()" etc.).
	raw, err := url.PathUnescape(p)
	if err != nil {
		return p
	}
	return uriEncode(raw, true)
}

// presign returns a query-signed URL for method on u, valid for ttl. Headers named
// in signedHeaders (besides host) must be sent by the caller exactly as given.
func presign(method string, u *url.URL, c credentials, region string, ttl time.Duration, hdr http.Header, now time.Time) string {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	names := []string{"host"}
	for k := range hdr {
		names = append(names, strings.ToLower(k))
	}
	q := u.Query()
	q.Set("X-Amz-Algorithm", algorithm)
	q.Set("X-Amz-Credential", c.accessKey+"/"+scope(date, region))
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", itoa(int64(ttl/time.Second)))
	if c.sessionToken != "" {
		q.Set("X-Amz-Security-Token", c.sessionToken)
	}
	block, signed := canonicalHeaders(hdr, u.Host, names)
	q.Set("X-Amz-SignedHeaders", signed)
	canon := strings.Join([]string{method, canonicalURI(u), canonicalQuery(q), block, signed, unsignedPayload}, "\n")
	sts := strings.Join([]string{algorithm, amzDate, scope(date, region), sha256Hex([]byte(canon))}, "\n")
	q.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(signingKey(c.secretKey, date, region), sts)))
	out := *u
	out.RawQuery = canonicalQuery(q)
	return out.String()
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
