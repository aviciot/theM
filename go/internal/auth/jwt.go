// Package auth implements local RS256 JWT validation and bearer token
// validation with a two-level cache (in-process L1 + Redis L2) backed by
// PostgreSQL. Token revocation is broadcast via Redis pub/sub so all pods
// invalidate their L1 within <1 s.
package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors returned by ValidateJWT. Callers should use errors.Is.
var (
	// ErrTokenExpired is returned when the token's exp claim is in the past.
	ErrTokenExpired = errors.New("auth: token expired")
	// ErrTokenMalformed is returned when the token cannot be parsed (wrong
	// number of segments, base64 decode failure, JSON decode failure).
	ErrTokenMalformed = errors.New("auth: token malformed")
	// ErrTokenSignature is returned when the RS256 signature does not verify
	// against the provided public key.
	ErrTokenSignature = errors.New("auth: token signature invalid")
)

// Claims holds the fields extracted from a validated JWT. Field names match
// the Python auth_service JSON payload so existing tokens work without
// re-issuing.
type Claims struct {
	UserID    int64    `json:"user_id"`
	Username  string   `json:"user_name"` // matches Python auth_service field name
	Email     string   `json:"email"`
	Roles     []string `json:"roles"`
	SessionID string   `json:"session_id,omitempty"`

	// Standard JWT fields (kept as int64 Unix timestamps for compatibility
	// with the Python auth_service issuer).
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	Issuer    string `json:"iss,omitempty"`
}

// jwtHeader is used only to confirm the algorithm before verifying.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// ValidateJWT parses and verifies a compact-serialised RS256 JWT.
// It performs signature verification using rsa.VerifyPKCS1v15 (stdlib only —
// no third-party JWT library) and checks the exp claim.
//
// Returns a pointer to the decoded Claims on success, or one of the sentinel
// errors (ErrTokenExpired, ErrTokenMalformed, ErrTokenSignature) on failure.
func ValidateJWT(tokenString string, pubKey *rsa.PublicKey) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrTokenMalformed, len(parts))
	}

	// ── Decode and parse header ───────────────────────────────────────────────
	headerBytes, err := base64urlDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header base64: %w", ErrTokenMalformed, err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: header JSON: %w", ErrTokenMalformed, err)
	}
	if !strings.EqualFold(header.Alg, "RS256") {
		return nil, fmt.Errorf("%w: unsupported algorithm %q (expected RS256)", ErrTokenMalformed, header.Alg)
	}

	// ── Decode and parse payload ──────────────────────────────────────────────
	payloadBytes, err := base64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload base64: %w", ErrTokenMalformed, err)
	}
	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload JSON: %w", ErrTokenMalformed, err)
	}

	// ── Verify RS256 signature ────────────────────────────────────────────────
	sigBytes, err := base64urlDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature base64: %w", ErrTokenMalformed, err)
	}

	// The signed message is "header.payload" (the first two dot-separated parts).
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))

	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		return nil, ErrTokenSignature
	}

	// ── Check expiry ──────────────────────────────────────────────────────────
	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return nil, ErrTokenExpired
	}

	return &claims, nil
}

// ParseRSAPublicKey decodes a PEM-encoded RSA public key in PKIX
// ("BEGIN PUBLIC KEY") or PKCS#1 ("BEGIN RSA PUBLIC KEY") format.
// Returns an error for any other PEM type or invalid DER content.
func ParseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("auth: failed to decode PEM block")
	}
	switch block.Type {
	case "PUBLIC KEY":
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parse PKIX public key: %w", err)
		}
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("auth: PEM does not contain an RSA public key")
		}
		return rsaKey, nil
	case "RSA PUBLIC KEY":
		pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parse PKCS1 public key: %w", err)
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("auth: unsupported PEM block type %q", block.Type)
	}
}

// GenerateRSAKeyPair generates a new 2048-bit RSA key pair. Used only in
// tests — not for production key generation.
func GenerateRSAKeyPair() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

// MarshalPublicKeyPEM encodes an RSA public key in PKIX PEM format.
func MarshalPublicKeyPEM(pub *rsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("auth: marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// MarshalPrivateKeyPEM encodes an RSA private key in PKCS#1 PEM format.
func MarshalPrivateKeyPEM(priv *rsa.PrivateKey) []byte {
	der := x509.MarshalPKCS1PrivateKey(priv)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

// IssueJWT creates and signs a compact RS256 JWT from the given claims.
// Used only in tests — production tokens are issued by the auth service.
func IssueJWT(claims Claims, privKey *rsa.PrivateKey) (string, error) {
	header := `{"alg":"RS256","typ":"JWT"}`
	headerEnc := base64.RawURLEncoding.EncodeToString([]byte(header))

	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	payloadEnc := base64.RawURLEncoding.EncodeToString(payloadBytes)

	signingInput := headerEnc + "." + payloadEnc
	digest := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("auth: sign JWT: %w", err)
	}
	sigEnc := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigEnc, nil
}

// base64urlDecode decodes a base64url-encoded string (no padding).
func base64urlDecode(s string) ([]byte, error) {
	// RFC 7515 uses unpadded base64url. Add padding if needed.
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// tokenHash returns the lowercase hex SHA-256 of rawToken, matching the hash
// stored in them.access_tokens by the Python platform.
func tokenHash(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return fmt.Sprintf("%x", h)
}
