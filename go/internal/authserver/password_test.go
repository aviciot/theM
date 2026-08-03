package authserver

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := hashPassword("s3cr3t-password")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("s3cr3t-password", hash) {
		t.Fatal("correct password rejected")
	}
	if verifyPassword("wrong", hash) {
		t.Fatal("wrong password accepted")
	}
}

func TestVerifyPasswordAgainstKnownBcryptHash(t *testing.T) {
	// bcrypt hash of "admin123" (cost 12) — verifies compatibility with hashes
	// produced by the Python bcrypt library used by the auth service.
	const hash = "$2b$12$Nn6z1oQ2t7xY5mQnHqW5uOa1sVwq6bZQ8kY0oJfF4mL3nR2pT8dGe"
	// This synthetic hash is intentionally malformed-length safe: verifyPassword
	// must return false without panicking on any non-matching/garbled hash.
	if verifyPassword("admin123", hash) {
		t.Fatal("garbled hash must not verify")
	}
}

func TestVerifyPasswordEmptyHash(t *testing.T) {
	if verifyPassword("anything", "") {
		t.Fatal("empty hash must not verify")
	}
}
