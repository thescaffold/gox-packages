package s3

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The worked examples from AWS's Signature V4 documentation for Amazon S3
// (examplebucket, 2013-05-24), so the signer is checked against AWS's own
// numbers and not only against a server written by the same hand.
var awsCreds = credentials{accessKey: "AKIAIOSFODNN7EXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}

var awsNow = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func TestSignHeaders_AWSGetObjectWithRangeExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	signHeaders(req, awsCreds, "us-east-1", emptySHA256, awsNow)
	const want = "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n %s\nwant\n %s", got, want)
	}
}

func TestPresign_AWSPresignedGetExample(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	got := presign(http.MethodGet, u, awsCreds, "us-east-1", 86400*time.Second, http.Header{}, awsNow)
	const sig = "X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if !strings.Contains(got, sig) {
		t.Fatalf("presigned URL = %s\nwant it to carry %s", got, sig)
	}
}

func TestCanonicalURIEncodesOnceAndKeepsSlashes(t *testing.T) {
	u := &url.URL{Path: "/b/ws/a/with space/文件.md", RawPath: uriEncode("/b/ws/a/with space/文件.md", true)}
	if got := canonicalURI(u); got != "/b/ws/a/with%20space/%E6%96%87%E4%BB%B6.md" {
		t.Fatalf("canonicalURI = %s", got)
	}
}
