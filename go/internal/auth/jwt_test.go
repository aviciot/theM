package auth_test

import (
	"crypto/rsa"
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
