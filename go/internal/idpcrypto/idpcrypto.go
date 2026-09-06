// Package idpcrypto provides AES-256-GCM encrypt/decrypt for IdP client secrets.
//
// Ciphertext format: hex(nonce || ciphertext) prefixed with "enc:".
// Plaintext values (no "enc:" prefix) are returned as-is so that secrets written
// before encryption was enabled continue to work — admin must re-save to encrypt.
//
// An empty key disables encryption: Encrypt returns the plaintext unchanged,
// Decrypt returns the input unchanged. This allows a gradual rollout.
package idpcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encPrefix = "enc:"

// ErrInvalidKey is returned when the key is not 32 bytes (64 hex chars).
var ErrInvalidKey = errors.New("idpcrypto: IDP_ENCRYPTION_KEY must be 64 hex characters (32 bytes)")

// ErrDecryptFailed is returned when decryption fails (wrong key or corrupt data).
var ErrDecryptFailed = errors.New("idpcrypto: decryption failed — wrong key or corrupt ciphertext; re-save IdP config")

// ParseKey decodes a 64-character hex string into a 32-byte AES-256 key.
// Returns nil when s is empty (encryption disabled).
func ParseKey(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	k, err := hex.DecodeString(s)
	if err != nil || len(k) != 32 {
		return nil, ErrInvalidKey
	}
	return k, nil
}

// Encrypt encrypts plaintext with AES-256-GCM and returns "enc:<hex(nonce||ciphertext)>".
// When key is nil (empty IDP_ENCRYPTION_KEY), it returns plaintext unchanged.
func Encrypt(key []byte, plaintext string) (string, error) {
	if len(key) == 0 {
		return plaintext, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("idpcrypto: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("idpcrypto: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("idpcrypto: rand: %w", err)
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + hex.EncodeToString(ct), nil
}

// Decrypt decrypts a value produced by Encrypt. Values without the "enc:" prefix
// are returned as-is (legacy plaintext — upgrade by re-saving the IdP config).
// When key is nil, all values are returned unchanged.
func Decrypt(key []byte, value string) (string, error) {
	if len(key) == 0 || !strings.HasPrefix(value, encPrefix) {
		return value, nil
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, encPrefix))
	if err != nil {
		return "", ErrDecryptFailed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("idpcrypto: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("idpcrypto: %w", err)
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", ErrDecryptFailed
	}
	pt, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(pt), nil
}
