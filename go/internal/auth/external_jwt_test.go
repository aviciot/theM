package auth_test

// Tests for ExternalJWTValidator — validates RS256 JWTs via JWKS.
// No HTTP calls; uses a fake jwks.Fetcher.

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
	"time"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/jwks"
)

// ── Test key (shared with jwks_test.go conceptually, but each package has its own) ─

var extTestKey *rsa.PrivateKey

func init() {
	var err error
	extTestKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("external_jwt_test: generate RSA key: " + err.Error())
	}
}

// fakeFetcherExt satisfies jwks.Fetcher for test use.
type fakeFetcherExt struct {
	doc *jwks.Document
}

func (f *fakeFetcherExt) FetchJWKS(_ context.Context, _ string) (*jwks.Document, error) {
	return f.doc, nil
}

func buildExtDoc(kid string) *jwks.Document {
	pub := &extTestKey.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	return &jwks.Document{Keys: []jwks.Key{{
		Kid: kid, Kty: "RSA", Alg: "RS256", Use: "sig", N: n, E: e,
	}}}
}

func buildExtToken(kid string, claims map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	pay, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString(hdr)
	p := base64.RawURLEncoding.EncodeToString(pay)
	signing := h + "." + p
	digest := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, extTestKey, crypto.SHA256, digest[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func newValidator() *auth.ExternalJWTValidator {
	cache := jwks.NewCache(&fakeFetcherExt{doc: buildExtDoc("k1")}, 0)
	return auth.NewExternalJWTValidator(cache)
}

func goodClaims() map[string]any {
	return map[string]any{
		"sub": "customer-123",
		"iss": "https://bank.example.com",
		"aud": "them-runtime",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
}

func goodCfg() auth.ExternalJWTConfig {
	return auth.ExternalJWTConfig{
		JWKSUri:  "https://bank.example.com/.well-known/jwks.json",
		Issuer:   "https://bank.example.com",
		Audience: "them-runtime",
	}
}

// EXT-1: valid token → returns sub.
func TestExternalJWT_ValidToken(t *testing.T) {
	v := newValidator()
	token := buildExtToken("k1", goodClaims())
	sub, err := v.Validate(context.Background(), token, goodCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub != "customer-123" {
		t.Errorf("sub=%q, want customer-123", sub)
	}
}

// EXT-2: expired token → error.
func TestExternalJWT_ExpiredToken(t *testing.T) {
	v := newValidator()
	claims := goodClaims()
	claims["exp"] = time.Now().Add(-1 * time.Minute).Unix()
	token := buildExtToken("k1", claims)
	_, err := v.Validate(context.Background(), token, goodCfg())
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got %v", err)
	}
}

// EXT-3: wrong issuer → error.
func TestExternalJWT_WrongIssuer(t *testing.T) {
	v := newValidator()
	claims := goodClaims()
	claims["iss"] = "https://attacker.example.com"
	token := buildExtToken("k1", claims)
	_, err := v.Validate(context.Background(), token, goodCfg())
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("expected issuer error, got %v", err)
	}
}

// EXT-4: wrong audience → error.
func TestExternalJWT_WrongAudience(t *testing.T) {
	v := newValidator()
	claims := goodClaims()
	claims["aud"] = "wrong-service"
	token := buildExtToken("k1", claims)
	_, err := v.Validate(context.Background(), token, goodCfg())
	if err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("expected audience error, got %v", err)
	}
}

// EXT-5: aud validation skipped when cfg.Audience is empty.
func TestExternalJWT_SkipAudCheck(t *testing.T) {
	v := newValidator()
	claims := goodClaims()
	claims["aud"] = "anything"
	token := buildExtToken("k1", claims)
	cfg := goodCfg()
	cfg.Audience = ""
	sub, err := v.Validate(context.Background(), token, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub != "customer-123" {
		t.Errorf("sub=%q, want customer-123", sub)
	}
}

// EXT-6: missing sub → error.
func TestExternalJWT_MissingSub(t *testing.T) {
	v := newValidator()
	claims := goodClaims()
	delete(claims, "sub")
	token := buildExtToken("k1", claims)
	_, err := v.Validate(context.Background(), token, goodCfg())
	if err == nil || !strings.Contains(err.Error(), "sub") {
		t.Fatalf("expected sub error, got %v", err)
	}
}

// EXT-7: aud as string (not array) is accepted.
func TestExternalJWT_AudAsString(t *testing.T) {
	v := newValidator()
	claims := goodClaims()
	claims["aud"] = "them-runtime" // string, not []string
	token := buildExtToken("k1", claims)
	sub, err := v.Validate(context.Background(), token, goodCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub != "customer-123" {
		t.Errorf("sub=%q, want customer-123", sub)
	}
}
