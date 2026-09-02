package authserver

// JWKS-based RS256 ID token signature verification with TTL-keyed cache.
//
// The IdP's JWKS endpoint is fetched at most once per TTL window per jwks_uri.
// On an unknown kid the cache is bypassed and the endpoint is re-fetched once
// (supports IdP key rotation without requiring a restart).
//
// No third-party OIDC or JWKS library is used — stdlib only.

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// jwksDocument is the JSON Web Key Set returned by the IdP.
type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// jwk is a minimal JSON Web Key (RFC 7517) — only the RSA fields used for
// RS256 signature verification are decoded.
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"` // base64url-encoded RSA modulus
	E   string `json:"e"` // base64url-encoded RSA public exponent
}

// rsaPublicKey converts a JWK to an *rsa.PublicKey.
// Returns an error if the key type is not RSA or if the modulus/exponent
// are missing or cannot be decoded.
func (k *jwk) rsaPublicKey() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("jwks: key %q has kty=%q, expected RSA", k.Kid, k.Kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil || len(nBytes) == 0 {
		return nil, fmt.Errorf("jwks: key %q: invalid modulus (n)", k.Kid)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil || len(eBytes) == 0 {
		return nil, fmt.Errorf("jwks: key %q: invalid exponent (e)", k.Kid)
	}
	n := new(big.Int).SetBytes(nBytes)
	// Exponent is big-endian; convert to int.
	var e int
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("jwks: key %q: exponent is zero", k.Kid)
	}
	pub := &rsa.PublicKey{N: n, E: e}
	// Sanity-check the key size — reject tiny keys that cannot be used safely.
	if pub.N.BitLen() < 2048 {
		return nil, fmt.Errorf("jwks: key %q: RSA key too small (%d bits)", k.Kid, pub.N.BitLen())
	}
	return pub, nil
}

// jwksFetcher is injectable so tests can provide a static JWKS without an
// outbound HTTP call.
type jwksFetcher interface {
	FetchJWKS(ctx context.Context, jwksURI string) (*jwksDocument, error)
}

// httpJWKSFetcher uses h.httpClient to download the JWKS.
type httpJWKSFetcher struct {
	client *http.Client
}

func (f *httpJWKSFetcher) FetchJWKS(ctx context.Context, jwksURI string) (*jwksDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, fmt.Errorf("jwks: build request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: endpoint returned %d", resp.StatusCode)
	}
	var doc jwksDocument
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256*1024)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	return &doc, nil
}

// ── JWKS TTL cache ────────────────────────────────────────────────────────────

const defaultJWKSCacheTTL = 5 * time.Minute

// jwksCacheEntry holds a fetched document and when it expires.
type jwksCacheEntry struct {
	doc       *jwksDocument
	expiresAt time.Time
}

// jwksCache wraps a jwksFetcher and caches responses per jwks_uri for ttl.
// On an unknown kid it skips the cache and re-fetches once to support key rotation.
// The cache is safe for concurrent use.
type jwksCache struct {
	inner   jwksFetcher
	ttl     time.Duration
	entries sync.Map // map[string]*jwksCacheEntry
}

func newJWKSCache(inner jwksFetcher, ttl time.Duration) *jwksCache {
	if ttl <= 0 {
		ttl = defaultJWKSCacheTTL
	}
	return &jwksCache{inner: inner, ttl: ttl}
}

// FetchJWKS returns a cached document if still fresh, otherwise fetches a fresh one.
func (c *jwksCache) FetchJWKS(ctx context.Context, jwksURI string) (*jwksDocument, error) {
	if v, ok := c.entries.Load(jwksURI); ok {
		entry := v.(*jwksCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.doc, nil
		}
	}
	return c.fetchAndStore(ctx, jwksURI)
}

// fetchFresh always fetches from the upstream fetcher, bypassing and replacing the cache.
// Used when a kid is not found in a cached document (key rotation path).
func (c *jwksCache) fetchFresh(ctx context.Context, jwksURI string) (*jwksDocument, error) {
	return c.fetchAndStore(ctx, jwksURI)
}

func (c *jwksCache) fetchAndStore(ctx context.Context, jwksURI string) (*jwksDocument, error) {
	doc, err := c.inner.FetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}
	c.entries.Store(jwksURI, &jwksCacheEntry{doc: doc, expiresAt: time.Now().Add(c.ttl)})
	return doc, nil
}

// idTokenHeader is the decoded JWT header fields needed for JWKS lookup.
type idTokenHeader struct {
	Kid string `json:"kid"`
	Alg string `json:"alg"`
}

// verifyRS256IDToken fetches (or loads from cache) the IdP's JWKS, selects the
// key matching the id_token's "kid" header, verifies the RS256 signature, and
// then parses and validates the claims.
//
// If the fetcher is a *jwksCache and the kid is not found in the cached document,
// the cache is bypassed and the JWKS is re-fetched once to handle IdP key rotation.
//
// Supported algorithm: RS256 only. Tokens with any other alg are rejected.
func verifyRS256IDToken(ctx context.Context, fetcher jwksFetcher, jwksURI, idToken string) (*idTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("id_token: expected 3 segments")
	}

	// 1. Decode and validate the header.
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("id_token: header base64 decode: %w", err)
	}
	var hdr idTokenHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("id_token: header JSON decode: %w", err)
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("id_token: unsupported algorithm %q, want RS256", hdr.Alg)
	}

	// 2. Fetch the JWKS and find the key matching kid.
	doc, err := fetcher.FetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, fmt.Errorf("id_token: %w", err)
	}
	matched := findKey(doc, hdr.Kid)

	// 3. If kid not found and fetcher is a cache, re-fetch once (key rotation path).
	if matched == nil {
		if cache, ok := fetcher.(*jwksCache); ok {
			doc, err = cache.fetchFresh(ctx, jwksURI)
			if err != nil {
				return nil, fmt.Errorf("id_token: re-fetch: %w", err)
			}
			matched = findKey(doc, hdr.Kid)
		}
	}

	if matched == nil {
		return nil, fmt.Errorf("id_token: no matching key for kid=%q in JWKS", hdr.Kid)
	}
	pub, err := matched.rsaPublicKey()
	if err != nil {
		return nil, err
	}

	// 4. Verify RS256 signature: SHA-256 hash of "header.payload", then RSA PKCS1v15 verify.
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("id_token: signature base64 decode: %w", err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sigBytes); err != nil {
		return nil, fmt.Errorf("id_token: signature verification failed")
	}

	// 5. Decode and validate the payload claims.
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("id_token: payload base64 decode: %w", err)
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("id_token: payload JSON decode: %w", err)
	}
	if claims.Sub == "" {
		return nil, fmt.Errorf("id_token: missing sub claim")
	}
	if claims.Email == "" {
		return nil, fmt.Errorf("id_token: missing email claim")
	}
	return &claims, nil
}

// findKey returns the first JWK whose kid matches kid, or the first key if kid
// is empty. Returns nil if no match is found.
func findKey(doc *jwksDocument, kid string) *jwk {
	for i := range doc.Keys {
		k := &doc.Keys[i]
		if kid == "" || k.Kid == kid {
			return k
		}
	}
	return nil
}
