package storage_test

import (
	"testing"

	"github.com/aviciot/them/internal/storage"
)

// TestNew_InvalidEndpoint verifies that a malformed endpoint URL returns an error.
func TestNew_InvalidEndpoint(t *testing.T) {
	_, err := storage.New(storage.Config{
		Endpoint:  "://bad-url",
		AccessKey: "key",
		SecretKey: "secret",
	})
	if err == nil {
		t.Fatal("expected error for bad endpoint, got nil")
	}
}

// TestNew_ValidEndpoint verifies that a well-formed HTTP endpoint constructs without error.
func TestNew_ValidEndpoint(t *testing.T) {
	c, err := storage.New(storage.Config{
		Endpoint:         "http://localhost:9000",
		AccessKey:        "minioadmin",
		SecretKey:        "minioadmin",
		QuarantineBucket: "them-quarantine",
		ArtifactsBucket:  "them-artifacts",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestNew_HTTPSEndpoint verifies that an HTTPS endpoint sets Secure=true without error.
func TestNew_HTTPSEndpoint(t *testing.T) {
	_, err := storage.New(storage.Config{
		Endpoint:  "https://s3.example.com",
		AccessKey: "key",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error for HTTPS endpoint: %v", err)
	}
}
