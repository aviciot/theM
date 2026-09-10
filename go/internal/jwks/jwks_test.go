package jwks_test

// Tests for jwks.VerifyRS256 using static test keys and fake fetcher.
// No HTTP calls are made. Tests mirror the authserver/oidc_jwks_test.go pattern
// but use exported types and the shared package API.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/jwks"
)

// ── Test key ──────────────────────────────────────────────────────────────────

var testRSAKey *rsa.PrivateKey

func init() {
	var err error
	testRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("jwks_test: generate RSA key: " + err.Error())
	}
}

// ── Fake fetcher ─────────────────────────────────────────────────────────────

type fakeFetcher struct {
	doc *jwks.Document
}

func (f *fakeFetcher) FetchJWKS(_ context.Context, _ string) (*jwks.Document, error) {
	return f.doc, nil
}

// buildDoc builds a jwks.Document from testRSAKey with the given kid.
func buildDoc(kid string) *jwks.Document {
	pub := &testRSAKey.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	return &jwks.Document{Keys: []jwks.Key{{
		Kid: kid, Kty: "RSA", Alg: "RS256", Use: "sig", N: n, E: e,
	}}}
}

// buildToken builds a minimal RS256 JWT with the given header kid and payload.
func buildToken(kid string, payload map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	pay, _ := json.Marshal(payload)
	h := base64.RawURLEncoding.EncodeToString(hdr)
	p := base64.RawURLEncoding.EncodeToString(pay)
	signing := h + "." + p
	digest := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, testRSAKey, crypto.SHA256, digest[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// JW-1: valid RS256 token → payload bytes returned.
func TestVerifyRS256_ValidToken(t *testing.T) {
	fetcher := &fakeFetcher{doc: buildDoc("k1")}
	token := buildToken("k1", map[string]any{"sub": "user42", "iss": "https://idp.example"})
	payload, err := jwks.VerifyRS256(context.Background(), fetcher, "https://idp/.well-known/jwks.json", token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if claims["sub"] != "user42" {
		t.Errorf("sub=%v, want user42", claims["sub"])
	}
}

// JW-2: tampered signature → error.
func TestVerifyRS256_TamperedSignature(t *testing.T) {
	fetcher := &fakeFetcher{doc: buildDoc("k1")}
	token := buildToken("k1", map[string]any{"sub": "user42"})
	parts := strings.Split(token, ".")
	parts[2] = base64.RawURLEncoding.EncodeToString([]byte("badsig"))
	_, err := jwks.VerifyRS256(context.Background(), fetcher, "u", strings.Join(parts, "."))
	if err == nil {
		t.Fatal("expected error for tampered signature, got nil")
	}
}

// JW-3: unsupported algorithm (HS256) → error.
func TestVerifyRS256_UnsupportedAlg(t *testing.T) {
	// Build token with HS256 alg header but RS256-signed body — alg check fires first.
	hdr, _ := json.Marshal(map[string]string{"alg": "HS256", "kid": "k1"})
	pay, _ := json.Marshal(map[string]string{"sub": "x"})
	h := base64.RawURLEncoding.EncodeToString(hdr)
	p := base64.RawURLEncoding.EncodeToString(pay)
	token := h + "." + p + ".fakesig"
	fetcher := &fakeFetcher{doc: buildDoc("k1")}
	_, err := jwks.VerifyRS256(context.Background(), fetcher, "u", token)
	if err == nil {
		t.Fatal("expected error for HS256 alg, got nil")
	}
	if !strings.Contains(err.Error(), "RS256") {
		t.Errorf("error should mention RS256, got: %v", err)
	}
}

// JW-4: unknown kid in cached doc — fake cache re-fetches with updated doc.
func TestVerifyRS256_KeyRotation(t *testing.T) {
	// First doc has kid=k1, second (post-rotation) has kid=k2.
	calls := 0
	docs := []*jwks.Document{buildDoc("k1"), buildDoc("k2")}
	fakeCacheInner := &countingFetcher{docs: docs, calls: &calls}

	cache := jwks.NewCache(fakeCacheInner, 0)
	// Prime with k1.
	primeToken := buildToken("k1", map[string]any{"sub": "u1"})
	if _, err := jwks.VerifyRS256(context.Background(), cache, "u", primeToken); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Token signed with k2 — cache miss → rotation re-fetch.
	rotatedToken := buildToken("k2", map[string]any{"sub": "u2"})
	payload, err := jwks.VerifyRS256(context.Background(), cache, "u", rotatedToken)
	if err != nil {
		t.Fatalf("rotation: %v", err)
	}
	var claims map[string]any
	json.Unmarshal(payload, &claims)
	if claims["sub"] != "u2" {
		t.Errorf("sub=%v, want u2", claims["sub"])
	}
}

// JW-5: malformed token (2 segments) → error.
func TestVerifyRS256_MalformedToken(t *testing.T) {
	fetcher := &fakeFetcher{doc: buildDoc("k1")}
	_, err := jwks.VerifyRS256(context.Background(), fetcher, "u", "header.payload")
	if err == nil {
		t.Fatal("expected error for 2-segment token")
	}
}

// countingFetcher cycles through a list of documents, returning one per call.
type countingFetcher struct {
	docs  []*jwks.Document
	calls *int
}

func (f *countingFetcher) FetchJWKS(_ context.Context, _ string) (*jwks.Document, error) {
	i := *f.calls
	if i < len(f.docs) {
		*f.calls++
		return f.docs[i], nil
	}
	return f.docs[len(f.docs)-1], nil
}
