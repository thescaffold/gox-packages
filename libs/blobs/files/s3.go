package files

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Provider stores files in AWS S3 or any S3-compatible store (MinIO, Wasabi, etc.).
//
// Fields:
//   - Bucket   — required; S3 bucket name
//   - Region   — AWS region (default: us-east-1)
//   - Endpoint — optional custom endpoint for S3-compatible stores
//   - AccessKey / SecretKey — static credentials; if empty, the default credential chain is used
type S3Provider struct {
	Bucket    string
	Region    string
	Endpoint  string
	AccessKey string
	SecretKey string
}

func (p *S3Provider) client() (*s3.Client, error) {
	region := p.Region
	if region == "" {
		region = "us-east-1"
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if p.AccessKey != "" && p.SecretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(p.AccessKey, p.SecretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("blobs: s3 config: %w", err)
	}

	clientOpts := []func(*s3.Options){}
	if p.Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(p.Endpoint)
			o.UsePathStyle = true
		})
	}

	return s3.NewFromConfig(cfg, clientOpts...), nil
}

// Upload stores r into the configured bucket under {bucket}/{name} and returns the public URL.
func (p *S3Provider) Upload(r io.Reader, name, bucket string, _ []string) (string, error) {
	if bucket == "" {
		bucket = p.Bucket
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("blobs: s3 read: %w", err)
	}

	client, err := p.client()
	if err != nil {
		return "", err
	}

	key := name
	_, err = client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return "", fmt.Errorf("blobs: s3 put: %w", err)
	}

	region := p.Region
	if region == "" {
		region = "us-east-1"
	}
	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, key)
	if p.Endpoint != "" {
		url = fmt.Sprintf("%s/%s/%s", p.Endpoint, bucket, key)
	}
	return url, nil
}

// Download retrieves the object identified by id (treated as the S3 key).
func (p *S3Provider) Download(id string) (io.ReadCloser, error) {
	client, err := p.client()
	if err != nil {
		return nil, err
	}

	out, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(p.Bucket),
		Key:    aws.String(id),
	})
	if err != nil {
		return nil, fmt.Errorf("blobs: s3 get: %w", err)
	}
	return out.Body, nil
}
