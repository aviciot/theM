package authserver

// Tests for JWKS-based RS256 id_token signature verification (Step 8).
// These tests exercise verifyRS256IDToken directly using a static fake jwksFetcher.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// fakeJWKSFetcher returns a pre-built jwksDocument without any HTTP call.
type fakeJWKSFetcher struct {
	doc *jwksDocument
	err error
}

func (f *fakeJWKSFetcher) FetchJWKS(_ context.Context, _ string) (*jwksDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.doc, nil
}

// buildTestJWKS builds a jwksDocument from testRSAKey with the given kid.
func buildTestJWKS(kid string) *jwksDocument {
	pub := &testRSAKey.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := pub.E
	var eBytes []byte
	for e > 0 {
		eBytes = append([]byte{byte(e & 0xff)}, eBytes...)
		e >>= 8
	}
	eStr := base64.RawURLEncoding.EncodeToString(eBytes)
	return &jwksDocument{Keys: []jwk{{
		Kid: kid,
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		N:   n,
		E:   eStr,
	}}}
}

// makeRS256Token builds a signed RS256 JWT from the given claims map.
func makeRS256Token(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hdrRaw, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	header := base64.RawURLEncoding.EncodeToString(hdrRaw)
	payRaw, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payRaw)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("makeRS256Token: sign failed: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func validClaims() map[string]any {
	return map[string]any{
		"sub":   "ext-user-1",
		"email": "test@example.com",
		"name":  "Test User",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
	}
}

// ── OIDC-13: valid RS256 token is accepted ────────────────────────────────────

func TestJWKS_ValidSignatureAccepted(t *testing.T) {
	fetcher := &fakeJWKSFetcher{doc: buildTestJWKS("key-1")}
	token := makeRS256Token(t, testRSAKey, "key-1", validClaims())

	claims, err := verifyRS256IDToken(context.Background(), fetcher, "https://example.com/jwks", token)
	if err != nil {
		t.Fatalf("OIDC-13: expected success, got: %v", err)
	}
	if claims.Email != "test@example.com" {
		t.Errorf("OIDC-13: expected email test@example.com, got %q", claims.Email)
	}
	if claims.Sub != "ext-user-1" {
		t.Errorf("OIDC-13: expected sub ext-user-1, got %q", claims.Sub)
	}
}

// ── OIDC-14: tampered signature is rejected ───────────────────────────────────

func TestJWKS_TamperedSignatureRejected(t *testing.T) {
	fetcher := &fakeJWKSFetcher{doc: buildTestJWKS("key-1")}
	token := makeRS256Token(t, testRSAKey, "key-1", validClaims())

	// Replace the signature segment with a different (invalid) base64 value.
	parts := splitJWTSegments(t, token)
	tampered := parts[0] + "." + parts[1] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	_, err := verifyRS256IDToken(context.Background(), fetcher, "https://example.com/jwks", tampered)
	if err == nil {
		t.Fatal("OIDC-14: expected signature verification to fail, got nil error")
	}
}

// ── OIDC-15: unknown kid is rejected ─────────────────────────────────────────

func TestJWKS_UnknownKidRejected(t *testing.T) {
	// JWKS has "key-1" but the token carries kid="other-key".
	fetcher := &fakeJWKSFetcher{doc: buildTestJWKS("key-1")}
	token := makeRS256Token(t, testRSAKey, "other-key", validClaims())

	_, err := verifyRS256IDToken(context.Background(), fetcher, "https://example.com/jwks", token)
	if err == nil {
		t.Fatal("OIDC-15: expected no matching key error, got nil")
	}
}

// ── OIDC-16: non-RS256 algorithm is rejected ─────────────────────────────────

func TestJWKS_WrongAlgRejected(t *testing.T) {
	fetcher := &fakeJWKSFetcher{doc: buildTestJWKS("key-1")}

	// Build a token with alg=HS256 in the header — verifyRS256IDToken must reject it
	// before attempting signature verification.
	hdrRaw, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT", "kid": "key-1"})
	header := base64.RawURLEncoding.EncodeToString(hdrRaw)
	payRaw, _ := json.Marshal(validClaims())
	payload := base64.RawURLEncoding.EncodeToString(payRaw)
	// Any signature value — algorithm check must fire first.
	token := header + "." + payload + ".fakesig"

	_, err := verifyRS256IDToken(context.Background(), fetcher, "https://example.com/jwks", token)
	if err == nil {
		t.Fatal("OIDC-16: expected algorithm rejection, got nil error")
	}
}

// ── OIDC-17: JWKS fetch error is propagated ──────────────────────────────────

func TestJWKS_FetchErrorPropagated(t *testing.T) {
	fetcher := &fakeJWKSFetcher{err: fmt.Errorf("network failure")}
	// Build a valid-looking RS256 header so we pass the alg check.
	hdrRaw, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "key-1"})
	header := base64.RawURLEncoding.EncodeToString(hdrRaw)
	payRaw, _ := json.Marshal(validClaims())
	payload := base64.RawURLEncoding.EncodeToString(payRaw)
	token := header + "." + payload + ".fakesig"

	_, err := verifyRS256IDToken(context.Background(), fetcher, "https://example.com/jwks", token)
	if err == nil {
		t.Fatal("OIDC-17: expected fetch error to propagate, got nil")
	}
}

// splitJWTSegments splits a JWT string into its three dot-separated segments.
func splitJWTSegments(t *testing.T, token string) [3]string {
	t.Helper()
	idx1 := dotIndex(token, 0)
	if idx1 < 0 {
		t.Fatalf("splitJWTSegments: missing first dot in %q", token)
	}
	idx2 := dotIndex(token, idx1+1)
	if idx2 < 0 {
		t.Fatalf("splitJWTSegments: missing second dot in %q", token)
	}
	return [3]string{token[:idx1], token[idx1+1 : idx2], token[idx2+1:]}
}

// dotIndex returns the index of the first '.' at or after start, or -1.
func dotIndex(s string, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
