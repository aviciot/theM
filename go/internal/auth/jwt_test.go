package auth_test

import (
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aviciot/them/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// Package-level test key pair (generated once per test run)
// ──────────────────────────────────────────────────────────────────────────────

var (
	testPrivKey *rsa.PrivateKey
	testPubKey  *rsa.PublicKey
)

func TestMain(m *testing.M) {
	priv, err := auth.GenerateRSAKeyPair()
	if err != nil {
		panic("jwt_test: failed to generate RSA key pair: " + err.Error())
	}
	testPrivKey = priv
	testPubKey = &priv.PublicKey
	m.Run()
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
// ──────────────────────────────────────────────────────────────────────────────

func validClaims() auth.Claims {
	return auth.Claims{
		UserID:    42,
		Username:  "alice",
		Email:     "alice@example.com",
		Roles:     []string{"admin"},
		ExpiresAt: time.Now().Add(10 * time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
		Issuer:    "test",
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ValidateJWT tests
// ──────────────────────────────────────────────────────────────────────────────

// Test 1: valid RS256 token returns correct Claims.
func TestValidateJWT_Valid(t *testing.T) {
	claims := validClaims()
	token, err := auth.IssueJWT(claims, testPrivKey)
	require.NoError(t, err)

	got, err := auth.ValidateJWT(token, testPubKey)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, claims.UserID, got.UserID)
	assert.Equal(t, claims.Username, got.Username)
	assert.Equal(t, claims.Email, got.Email)
	assert.Equal(t, claims.Roles, got.Roles)
	assert.Equal(t, claims.Issuer, got.Issuer)
	assert.Equal(t, claims.ExpiresAt, got.ExpiresAt)
}

// Test 2: expired token returns ErrTokenExpired.
func TestValidateJWT_Expired(t *testing.T) {
	claims := validClaims()
	claims.ExpiresAt = time.Now().Add(-1 * time.Minute).Unix() // expired

	token, err := auth.IssueJWT(claims, testPrivKey)
	require.NoError(t, err)

	_, err = auth.ValidateJWT(token, testPubKey)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrTokenExpired), "expected ErrTokenExpired, got: %v", err)
}

// Test 3: tampered signature returns ErrTokenSignature.
func TestValidateJWT_TamperedSignature(t *testing.T) {
	token, err := auth.IssueJWT(validClaims(), testPrivKey)
	require.NoError(t, err)

	// Replace the signature segment with garbage.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	parts[2] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	tampered := strings.Join(parts, ".")

	_, err = auth.ValidateJWT(tampered, testPubKey)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrTokenSignature), "expected ErrTokenSignature, got: %v", err)
}

// Test 4: malformed token (wrong number of segments) returns ErrTokenMalformed.
func TestValidateJWT_Malformed_MissingDot(t *testing.T) {
	_, err := auth.ValidateJWT("notavalidjwt", testPubKey)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrTokenMalformed), "expected ErrTokenMalformed, got: %v", err)
}

// Additional malformed case: only two segments.
func TestValidateJWT_Malformed_TwoSegments(t *testing.T) {
	_, err := auth.ValidateJWT("header.payload", testPubKey)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrTokenMalformed), "expected ErrTokenMalformed, got: %v", err)
}

// ──────────────────────────────────────────────────────────────────────────────
// ParseRSAPublicKey tests
// ──────────────────────────────────────────────────────────────────────────────

// Test 5: ParseRSAPublicKey with valid PKIX PEM succeeds.
func TestParseRSAPublicKey_Valid(t *testing.T) {
	pemBytes, err := auth.MarshalPublicKeyPEM(testPubKey)
	require.NoError(t, err)
	require.NotEmpty(t, pemBytes)

	got, err := auth.ParseRSAPublicKey(pemBytes)
	require.NoError(t, err)
	require.NotNil(t, got)

	// The parsed key should match our test key.
	assert.Equal(t, testPubKey.N, got.N)
	assert.Equal(t, testPubKey.E, got.E)
}

// Test 6: ParseRSAPublicKey with garbage returns error.
func TestParseRSAPublicKey_Garbage(t *testing.T) {
	_, err := auth.ParseRSAPublicKey([]byte("this is not valid PEM"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth:")
}

func TestParseRSAPublicKey_EmptyPEM(t *testing.T) {
	_, err := auth.ParseRSAPublicKey([]byte(""))
	require.Error(t, err)
}

func TestParseRSAPublicKey_WrongPEMType(t *testing.T) {
	pem := "-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----\n"
	_, err := auth.ParseRSAPublicKey([]byte(pem))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported PEM block type")
}

// ── ValidateHS256JWT tests ────────────────────────────────────────────────────

// buildHS256Token creates a signed HS256 JWT with the given payload and secret.
func buildHS256Token(t *testing.T, claims map[string]any, secret []byte) string {
	t.Helper()
	header := `{"alg":"HS256","typ":"JWT"}`
	headerEnc := base64url([]byte(header))
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	payloadEnc := base64url(payload)
	sigInput := headerEnc + "." + payloadEnc
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sigInput))
	sig := base64url(mac.Sum(nil))
	return sigInput + "." + sig
}

func base64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestValidateHS256JWT_Valid(t *testing.T) {
	secret := []byte("mysecret")
	now := time.Now().Unix()
	token := buildHS256Token(t, map[string]any{
		"sub": "7", "username": "alice", "role": "super_admin",
		"exp": now + 3600, "iat": now,
	}, secret)

	claims, err := auth.ValidateHS256JWT(token, secret)
	require.NoError(t, err)
	assert.Equal(t, int64(7), claims.UserID)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, []string{"super_admin"}, claims.Roles)
}

func TestValidateHS256JWT_Expired(t *testing.T) {
	secret := []byte("mysecret")
	token := buildHS256Token(t, map[string]any{
		"sub": "1", "username": "bob", "role": "user",
		"exp": time.Now().Unix() - 1,
	}, secret)
	_, err := auth.ValidateHS256JWT(token, secret)
	require.ErrorIs(t, err, auth.ErrTokenExpired)
}

func TestValidateHS256JWT_WrongSecret(t *testing.T) {
	token := buildHS256Token(t, map[string]any{
		"sub": "1", "username": "carol", "role": "user",
		"exp": time.Now().Unix() + 3600,
	}, []byte("correct-secret"))
	_, err := auth.ValidateHS256JWT(token, []byte("wrong-secret"))
	require.ErrorIs(t, err, auth.ErrTokenSignature)
}

func TestValidateHS256JWT_WrongAlgorithm(t *testing.T) {
	// RS256 token presented to HS256 validator must be rejected.
	privKey, _ := auth.GenerateRSAKeyPair()
	token, err := auth.IssueJWT(auth.Claims{
		UserID: 1, Username: "dave", ExpiresAt: time.Now().Unix() + 3600,
	}, privKey)
	require.NoError(t, err)
	_, err = auth.ValidateHS256JWT(token, []byte("any-secret"))
	require.ErrorIs(t, err, auth.ErrTokenMalformed)
}

func TestValidateHS256JWT_Malformed(t *testing.T) {
	_, err := auth.ValidateHS256JWT("not.a.jwt", []byte("secret"))
	// signature decode will fail or HMAC will mismatch
	require.Error(t, err)
}
