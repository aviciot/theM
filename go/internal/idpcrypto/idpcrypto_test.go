package idpcrypto_test

import (
	"strings"
	"testing"

	"github.com/aviciot/them/internal/idpcrypto"
)

// 32 zero bytes as hex — test key only, never use in production.
const testKeyHex = "0000000000000000000000000000000000000000000000000000000000000000"

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := idpcrypto.ParseKey(testKeyHex)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return k
}

// IDP-1: encrypt + decrypt round-trip returns original plaintext.
func TestIDPCrypto_RoundTrip(t *testing.T) {
	key := testKey(t)
	plain := "super-secret-client-secret"
	ct, err := idpcrypto.Encrypt(key, plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(ct, "enc:") {
		t.Fatalf("expected enc: prefix, got %q", ct)
	}
	got, err := idpcrypto.Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plain {
		t.Fatalf("round-trip mismatch: want %q got %q", plain, got)
	}
}

// IDP-2: each Encrypt call produces a different ciphertext (random nonce).
func TestIDPCrypto_NondeterministicCiphertext(t *testing.T) {
	key := testKey(t)
	ct1, _ := idpcrypto.Encrypt(key, "secret")
	ct2, _ := idpcrypto.Encrypt(key, "secret")
	if ct1 == ct2 {
		t.Fatal("two Encrypt calls produced identical ciphertext — nonce is not random")
	}
}

// IDP-3: Decrypt on a wrong key returns ErrDecryptFailed.
func TestIDPCrypto_WrongKey(t *testing.T) {
	key := testKey(t)
	ct, _ := idpcrypto.Encrypt(key, "secret")

	wrongKey, _ := idpcrypto.ParseKey("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	_, err := idpcrypto.Decrypt(wrongKey, ct)
	if err != idpcrypto.ErrDecryptFailed {
		t.Fatalf("expected ErrDecryptFailed, got %v", err)
	}
}

// IDP-4: nil key → Encrypt and Decrypt are both pass-through.
func TestIDPCrypto_NilKey_Passthrough(t *testing.T) {
	plain := "plaintext-secret"
	ct, err := idpcrypto.Encrypt(nil, plain)
	if err != nil {
		t.Fatalf("Encrypt(nil): %v", err)
	}
	if ct != plain {
		t.Fatalf("Encrypt(nil) should return plaintext unchanged, got %q", ct)
	}
	got, err := idpcrypto.Decrypt(nil, ct)
	if err != nil {
		t.Fatalf("Decrypt(nil): %v", err)
	}
	if got != plain {
		t.Fatalf("Decrypt(nil) mismatch: want %q got %q", plain, got)
	}
}

// IDP-5: legacy plaintext (no enc: prefix) passes through Decrypt unchanged.
func TestIDPCrypto_LegacyPlaintext_PassThrough(t *testing.T) {
	key := testKey(t)
	legacy := "plain-legacy-secret"
	got, err := idpcrypto.Decrypt(key, legacy)
	if err != nil {
		t.Fatalf("Decrypt(legacy): %v", err)
	}
	if got != legacy {
		t.Fatalf("legacy plaintext mismatch: want %q got %q", legacy, got)
	}
}

// IDP-6: ParseKey rejects wrong-length hex.
func TestIDPCrypto_ParseKey_WrongLength(t *testing.T) {
	_, err := idpcrypto.ParseKey("deadbeef") // 4 bytes, not 32
	if err != idpcrypto.ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey for short key, got %v", err)
	}
}

// IDP-7: ParseKey rejects non-hex input.
func TestIDPCrypto_ParseKey_NotHex(t *testing.T) {
	_, err := idpcrypto.ParseKey("not-hex-at-all")
	if err != idpcrypto.ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey for non-hex input, got %v", err)
	}
}

// IDP-8: ParseKey returns nil for empty string (encryption disabled).
func TestIDPCrypto_ParseKey_Empty(t *testing.T) {
	k, err := idpcrypto.ParseKey("")
	if err != nil {
		t.Fatalf("ParseKey empty: %v", err)
	}
	if k != nil {
		t.Fatalf("ParseKey empty should return nil key, got %v", k)
	}
}

// IDP-9: corrupt ciphertext (truncated) returns ErrDecryptFailed.
func TestIDPCrypto_CorruptCiphertext(t *testing.T) {
	key := testKey(t)
	_, err := idpcrypto.Decrypt(key, "enc:deadbeef")
	if err != idpcrypto.ErrDecryptFailed {
		t.Fatalf("expected ErrDecryptFailed for truncated ct, got %v", err)
	}
}
