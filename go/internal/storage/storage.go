// Package storage provides a thin S3-compatible object store client wrapping
// minio-go. The caller interacts only with bucket names and object keys; all
// MinIO connection details are encapsulated here.
//
// Two buckets are used:
//   - quarantine: holds uploaded bytes before a virus scan completes (1-hour TTL
//     lifecycle policy set externally via MinIO console or mc lifecycle)
//   - artifacts: holds confirmed-clean bytes served for download
package storage

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config holds the MinIO / S3 connection settings.
type Config struct {
	Endpoint         string // e.g. "http://them-minio:9000"
	AccessKey        string
	SecretKey        string
	QuarantineBucket string
	ArtifactsBucket  string
}

// Client wraps a minio.Client with the two bucket names.
type Client struct {
	mc               *minio.Client
	quarantineBucket string
	artifactsBucket  string
}

// New creates a Client from cfg. Returns an error if the MinIO SDK cannot
// be initialised (bad endpoint or credentials format).
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("storage: parse endpoint %q: %w", cfg.Endpoint, err)
	}
	useSSL := u.Scheme == "https"
	endpoint := u.Host // host:port without scheme

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: create minio client: %w", err)
	}
	return &Client{
		mc:               mc,
		quarantineBucket: cfg.QuarantineBucket,
		artifactsBucket:  cfg.ArtifactsBucket,
	}, nil
}

// PutQuarantine writes data to the quarantine bucket under key.
// contentType is the MIME type hint stored as object metadata.
func (c *Client) PutQuarantine(ctx context.Context, key string, data []byte, contentType string) error {
	return c.put(ctx, c.quarantineBucket, key, data, contentType)
}

// GetQuarantine retrieves bytes from the quarantine bucket.
func (c *Client) GetQuarantine(ctx context.Context, key string) ([]byte, error) {
	return c.get(ctx, c.quarantineBucket, key)
}

// DeleteQuarantine removes an object from the quarantine bucket.
// A missing object is not an error.
func (c *Client) DeleteQuarantine(ctx context.Context, key string) error {
	return c.delete(ctx, c.quarantineBucket, key)
}

// PutArtifact writes data to the confirmed-clean artifacts bucket.
func (c *Client) PutArtifact(ctx context.Context, key string, data []byte, contentType string) error {
	return c.put(ctx, c.artifactsBucket, key, data, contentType)
}

// GetArtifact retrieves bytes from the artifacts bucket.
func (c *Client) GetArtifact(ctx context.Context, key string) ([]byte, error) {
	return c.get(ctx, c.artifactsBucket, key)
}

// DeleteArtifact removes an object from the artifacts bucket.
func (c *Client) DeleteArtifact(ctx context.Context, key string) error {
	return c.delete(ctx, c.artifactsBucket, key)
}

// PresignArtifact returns a time-limited presigned GET URL for a confirmed-clean
// artifact. Callers should use a short expiry (minutes, not hours).
func (c *Client) PresignArtifact(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := c.mc.PresignedGetObject(ctx, c.artifactsBucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign %s/%s: %w", c.artifactsBucket, key, err)
	}
	return u.String(), nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (c *Client) put(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := c.mc.PutObject(ctx, bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("storage: put %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, bucket, key string) ([]byte, error) {
	obj, err := c.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %s/%s: %w", bucket, key, err)
	}
	defer obj.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(obj); err != nil {
		return nil, fmt.Errorf("storage: read %s/%s: %w", bucket, key, err)
	}
	return buf.Bytes(), nil
}

func (c *Client) delete(ctx context.Context, bucket, key string) error {
	err := c.mc.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		// Treat "NoSuchKey" as success — idempotent delete
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" {
			return nil
		}
		return fmt.Errorf("storage: delete %s/%s: %w", bucket, key, err)
	}
	return nil
}
