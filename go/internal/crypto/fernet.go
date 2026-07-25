// Package crypto provides Fernet-compatible symmetric encryption that is
// byte-for-byte compatible with Python's cryptography.fernet.Fernet.
//
// Wire format (binary, before base64url encoding):
//
//	Version    1 byte   always 0x80
//	Timestamp  8 bytes  big-endian uint64 — Unix seconds at encrypt time
//	IV        16 bytes  random AES-CBC initialization vector
//	Ciphertext N bytes  AES-128-CBC(PKCS7-padded plaintext); N is a multiple of 16
//	HMAC      32 bytes  HMAC-SHA256(Version+Timestamp+IV+Ciphertext) using signing_key
//
// Key splitting: a 32-byte raw key is split as:
//
//	signing_key    = key[0:16]   — used for HMAC-SHA256
//	encryption_key = key[16:32]  — used for AES-128-CBC
//
// Python key derivation: sha256(SECRET_KEY) yields 32 raw bytes; those bytes
// are then base64url-encoded to produce the Fernet key string (which Fernet
// decodes back to 32 bytes internally). Go DeriveKey returns the 32 raw bytes
// directly; callers do not need to base64-encode them.
//
// Storage prefix: Python's encrypt_value() prepends "enc:" to the base64url
// token before storing in the database. Decrypt and Encrypt in this package
// operate on the raw Fernet token (without the prefix). Use DecryptStored /
// EncryptStored for values read from / written to the database.
//
// Security notes:
//   - HMAC is verified with constant-time comparison before any decryption step
//     (prevents padding oracle attacks).
//   - Timestamp is embedded but NOT enforced as an expiry here (matches Python
//     usage without the optional ttl parameter). Callers that need key rotation
//     or token expiry must enforce it at a higher layer.
//   - Plaintext bytes are zeroed in Decrypt after the caller's return value is
//     built — callers are responsible for zeroing their own copies promptly.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors returned by Decrypt / DecryptStored.
var (
	// ErrInvalidToken is returned for any structurally malformed token:
	// wrong base64, wrong version byte, wrong length, invalid PKCS7 padding.
	ErrInvalidToken = errors.New("fernet: invalid token")

	// ErrHMACMismatch is returned when the HMAC verification fails.
	// This covers both wrong-key and tampered-token cases.
	ErrHMACMismatch = errors.New("fernet: HMAC mismatch — wrong key or tampered token")
)

const (
	fernetVersion = byte(0x80)
	versionLen    = 1
	timestampLen  = 8
	ivLen         = 16
	hmacLen       = 32
	// minTokenLen is version + timestamp + IV + (≥1 block of ciphertext) + HMAC.
	minTokenLen = versionLen + timestampLen + ivLen + aes.BlockSize + hmacLen

	// storedPrefix is prepended to Fernet tokens when stored in the database,
	// matching Python's _ENC_PREFIX = "enc:".
	storedPrefix = "enc:"
)

// DeriveKey returns the 32-byte Fernet key material derived from secretKey.
// The derivation is sha256(secretKey) — identical to Python's:
//
//	hashlib.sha256(settings.security.secret_key.encode()).digest()
//
// The returned bytes are used directly with Encrypt / Decrypt. Unlike Python,
// the caller does not need to base64-encode the key; this package handles that
// internally.
//
// The secretKey must not be empty. If it is, Encrypt/Decrypt return errors.
func DeriveKey(secretKey string) []byte {
	sum := sha256.Sum256([]byte(secretKey))
	return sum[:]
}

// Encrypt encrypts plaintext using Fernet (AES-128-CBC + HMAC-SHA256) and
// returns the base64url-encoded Fernet token. key must be exactly 32 bytes
// (from DeriveKey). The returned token does NOT include the "enc:" prefix.
//
// An empty plaintext (len == 0) returns ErrInvalidToken — matching Python's
// encrypt_value guard which returns the input unchanged for empty strings.
// Callers that need to store a confirmed "no key" state should use a nil
// database column rather than encrypting an empty string.
func Encrypt(key, plaintext []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("fernet: key must be 32 bytes, got %d", len(key))
	}
	if len(plaintext) == 0 {
		return "", ErrInvalidToken
	}

	encryptionKey := key[16:32]

	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("fernet: generate IV: %w", err)
	}

	padded := pkcs7Pad(plaintext, aes.BlockSize)

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("fernet: create cipher: %w", err)
	}
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	ts := nowTimestamp()
	msg := buildMsg(iv, ciphertext, ts)
	mac := computeHMAC(key[:16], msg)

	raw := append(msg, mac...)
	return base64.URLEncoding.EncodeToString(raw), nil
}

// Decrypt decrypts a Fernet token (base64url, WITHOUT the "enc:" prefix).
// Returns the plaintext or an error. key must be exactly 32 bytes (from DeriveKey).
//
// HMAC verification is performed before any decryption step. A bad HMAC
// returns ErrHMACMismatch regardless of whether the cause is a wrong key or
// a tampered ciphertext.
//
// Callers MUST treat any returned error as "data present but unreadable"
// and must not surface the error message to API clients in a way that
// reveals cipher structure.
func Decrypt(key []byte, token string) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("fernet: key must be 32 bytes, got %d", len(key))
	}

	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 decode: %v", ErrInvalidToken, err)
	}

	if len(raw) < minTokenLen {
		return nil, fmt.Errorf("%w: token too short (%d bytes, minimum %d)", ErrInvalidToken, len(raw), minTokenLen)
	}

	if raw[0] != fernetVersion {
		return nil, fmt.Errorf("%w: unsupported version 0x%02x", ErrInvalidToken, raw[0])
	}

	// Ciphertext length must be a multiple of the AES block size.
	ciphertextLen := len(raw) - versionLen - timestampLen - ivLen - hmacLen
	if ciphertextLen <= 0 || ciphertextLen%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: ciphertext length %d is not a positive multiple of %d", ErrInvalidToken, ciphertextLen, aes.BlockSize)
	}

	signingKey := key[:16]
	encryptionKey := key[16:32]

	// Slice out components.
	msgEnd := len(raw) - hmacLen
	msg := raw[:msgEnd]
	storedMAC := raw[msgEnd:]
	iv := raw[versionLen+timestampLen : versionLen+timestampLen+ivLen]
	ciphertext := raw[versionLen+timestampLen+ivLen : msgEnd]

	// Verify HMAC before decryption (constant-time).
	expectedMAC := computeHMAC(signingKey, msg)
	if subtle.ConstantTimeCompare(storedMAC, expectedMAC) != 1 {
		return nil, ErrHMACMismatch
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("fernet: create cipher: %w", err)
	}
	plainPadded := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plainPadded, ciphertext)

	plain, err := pkcs7Unpad(plainPadded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return plain, nil
}

// EncryptStored encrypts plaintext and returns the "enc:"-prefixed string
// suitable for storage in the database. This matches the output of Python's
// encrypt_value().
func EncryptStored(key []byte, plaintext string) (string, error) {
	token, err := Encrypt(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return storedPrefix + token, nil
}

// DecryptStored decrypts a database-stored value that was produced by
// Python's encrypt_value() or Go's EncryptStored(). It strips the "enc:"
// prefix before calling Decrypt.
//
// If stored is empty or does not start with "enc:", it is returned as-is —
// matching Python's decrypt_value() which returns the input unchanged when
// the prefix is absent. This handles legacy unencrypted values gracefully.
//
// Returns ("", ErrHMACMismatch) or ("", ErrInvalidToken) on failure.
// The caller should treat any error as "value present but unreadable".
func DecryptStored(key []byte, stored string) (string, error) {
	if stored == "" || !hasPrefix(stored, storedPrefix) {
		// No prefix: Python returns value as-is (legacy / unencrypted).
		return stored, nil
	}
	token := stored[len(storedPrefix):]
	plain, err := Decrypt(key, token)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ─── internal helpers ────────────────────────────────────────────────────────

// buildMsg constructs Version + Timestamp + IV + Ciphertext.
func buildMsg(iv, ciphertext []byte, ts uint64) []byte {
	msg := make([]byte, versionLen+timestampLen+ivLen+len(ciphertext))
	msg[0] = fernetVersion
	putBigEndianUint64(msg[1:9], ts)
	copy(msg[9:25], iv)
	copy(msg[25:], ciphertext)
	return msg
}

// computeHMAC returns HMAC-SHA256(signingKey, data).
func computeHMAC(signingKey, data []byte) []byte {
	h := hmac.New(sha256.New, signingKey)
	h.Write(data)
	return h.Sum(nil)
}

// pkcs7Pad pads data to a multiple of blockSize using PKCS7.
// blockSize must be ≤ 255 (enforced by the PKCS7 spec).
func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	padded := make([]byte, len(data)+padLen)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	return padded
}

// pkcs7Unpad removes PKCS7 padding and validates it.
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("pkcs7: empty input")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, fmt.Errorf("pkcs7: invalid padding byte %d", padLen)
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, errors.New("pkcs7: inconsistent padding bytes")
		}
	}
	return data[:len(data)-padLen], nil
}

// putBigEndianUint64 encodes v into exactly 8 bytes of b in big-endian order.
func putBigEndianUint64(b []byte, v uint64) {
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
}

// hasPrefix reports whether s starts with prefix.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// nowTimestamp returns the current Unix time as uint64, used when building
// a Fernet token's timestamp field. Defined as a variable so tests can
// substitute a deterministic clock.
var nowTimestamp = func() uint64 {
	return uint64(time.Now().Unix())
}
