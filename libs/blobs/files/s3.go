package files

import (
	"errors"
	"io"
)

// S3Provider is a stub for AWS S3 (or compatible) storage.
// Wire in the AWS SDK in the host app and replace this with a real implementation.
type S3Provider struct {
	Bucket   string
	Region   string
	Endpoint string
}

var errNotImplemented = errors.New("blobs: S3Provider not implemented — wire aws-sdk-go in the host app")

func (p *S3Provider) Upload(_ io.Reader, _, _ string, _ []string) (string, error) {
	return "", errNotImplemented
}

func (p *S3Provider) Download(_ string) (io.ReadCloser, error) {
	return nil, errNotImplemented
}
