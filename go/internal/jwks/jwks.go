// Package jwks provides RS256 JWT signature verification using JWKS endpoints.
//
// This package is shared by the authserver (OIDC SSO dashboard login) and the
// runtime entry-point admission pipeline (external_jwt EPs). The two consumers
// use different claims structs and validation rules; only the JWKS fetch/cache
// and signature verification primitives are shared here.
//
// Algorithm support: RS256 only. Tokens with any other algorithm are rejected
// with an explicit error. ES256 support is a documented future addition.
//
// No third-party JWKS or JWT library is used — stdlib crypto only.
package jwks

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

// ── Key types ─────────────────────────────────────────────────────────────────

// Document is the JSON Web Key Set returned by an IdP's JWKS endpoint.
type Document struct {
	Keys []Key `json:"keys"`
}

// Key is a minimal JSON Web Key (RFC 7517).  Only RSA fields used for RS256
// signature verification are decoded; other key types are silently skipped
// during lookup (FindKey will not return them).
type Key struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"` // base64url-encoded RSA modulus
	E   string `json:"e"` // base64url-encoded RSA public exponent
}

// RSAPublicKey converts a JWK to an *rsa.PublicKey.
// Returns an error if the key type is not RSA, or if N/E are missing or
// cannot be decoded.  Rejects keys smaller than 2048 bits.
func (k *Key) RSAPublicKey() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("jwks: key %q has kty=%q, want RSA", k.Kid, k.Kty)
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
	var e int
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("jwks: key %q: exponent is zero", k.Kid)
	}
	pub := &rsa.PublicKey{N: n, E: e}
	if pub.N.BitLen() < 2048 {
		return nil, fmt.Errorf("jwks: key %q: RSA key too small (%d bits, min 2048)", k.Kid, pub.N.BitLen())
	}
	return pub, nil
}

// FindKey returns the first Key whose kid matches kid, or the first Key when
// kid is empty.  Returns nil when no match is found.
func FindKey(doc *Document, kid string) *Key {
	for i := range doc.Keys {
		k := &doc.Keys[i]
		if kid == "" || k.Kid == kid {
			return k
		}
	}
	return nil
}

// ── Fetcher ───────────────────────────────────────────────────────────────────

// Fetcher is injectable so tests can provide a static JWKS without an HTTP call.
type Fetcher interface {
	FetchJWKS(ctx context.Context, jwksURI string) (*Document, error)
}

// HTTPFetcher uses an http.Client to download a JWKS document.
type HTTPFetcher struct {
	Client *http.Client
}

func (f *HTTPFetcher) FetchJWKS(ctx context.Context, jwksURI string) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, fmt.Errorf("jwks: build request: %w", err)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: fetch %s: %w", jwksURI, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: endpoint %s returned HTTP %d", jwksURI, resp.StatusCode)
	}
	var doc Document
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256*1024)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	return &doc, nil
}

// ── TTL cache ─────────────────────────────────────────────────────────────────

const DefaultCacheTTL = 5 * time.Minute

type cacheEntry struct {
	doc       *Document
	expiresAt time.Time
}

// Cache wraps a Fetcher and caches results per jwks_uri.
// On an unknown kid it bypasses the cache and re-fetches once (key rotation).
// Safe for concurrent use.
type Cache struct {
	inner   Fetcher
	ttl     time.Duration
	entries sync.Map // map[string]*cacheEntry
}

// NewCache returns a Cache backed by inner with the given TTL.
// A zero or negative TTL uses DefaultCacheTTL.
func NewCache(inner Fetcher, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Cache{inner: inner, ttl: ttl}
}

// NewDefaultCache returns a Cache backed by a default HTTP client.
func NewDefaultCache() *Cache {
	return NewCache(&HTTPFetcher{Client: &http.Client{Timeout: 10 * time.Second}}, DefaultCacheTTL)
}

// FetchJWKS returns a cached document if still fresh, else fetches a new one.
func (c *Cache) FetchJWKS(ctx context.Context, jwksURI string) (*Document, error) {
	if v, ok := c.entries.Load(jwksURI); ok {
		entry := v.(*cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.doc, nil
		}
	}
	return c.fetchAndStore(ctx, jwksURI)
}

// FetchFresh always fetches from upstream, bypassing and replacing the cache.
// Used when a kid is not found in the cached document (key rotation path).
func (c *Cache) FetchFresh(ctx context.Context, jwksURI string) (*Document, error) {
	return c.fetchAndStore(ctx, jwksURI)
}

func (c *Cache) fetchAndStore(ctx context.Context, jwksURI string) (*Document, error) {
	doc, err := c.inner.FetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}
	c.entries.Store(jwksURI, &cacheEntry{doc: doc, expiresAt: time.Now().Add(c.ttl)})
	return doc, nil
}

// ── Signature verification ────────────────────────────────────────────────────

// Header is the decoded JWT header fields needed for JWKS key lookup.
type Header struct {
	Kid string `json:"kid"`
	Alg string `json:"alg"`
}

// VerifyRS256 verifies an RS256-signed JWT using the given Fetcher/Cache.
//
// On success it returns the validated raw payload bytes.  The caller is
// responsible for parsing claims out of the payload.
//
// Supported algorithm: RS256 only.  Any other alg returns an error immediately
// so the caller does not attempt verification with the wrong algorithm.
//
// Key rotation: if fetcher is a *Cache and the kid is not found in the cached
// document, the cache is bypassed and the JWKS is re-fetched once.
func VerifyRS256(ctx context.Context, fetcher Fetcher, jwksURI, rawToken string) ([]byte, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("jwks: token must have 3 segments")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("jwks: header base64: %w", err)
	}
	var hdr Header
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("jwks: header JSON: %w", err)
	}
	// Reject non-RS256 tokens explicitly; ES256 is a documented future addition.
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("jwks: unsupported algorithm %q; only RS256 is supported", hdr.Alg)
	}

	doc, err := fetcher.FetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, fmt.Errorf("jwks: fetch: %w", err)
	}
	matched := FindKey(doc, hdr.Kid)

	// kid not found: try a fresh fetch once (handles IdP key rotation).
	if matched == nil {
		if cache, ok := fetcher.(*Cache); ok {
			doc, err = cache.FetchFresh(ctx, jwksURI)
			if err != nil {
				return nil, fmt.Errorf("jwks: re-fetch after rotation: %w", err)
			}
			matched = FindKey(doc, hdr.Kid)
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("jwks: no key for kid=%q in JWKS", hdr.Kid)
	}

	pub, err := matched.RSAPublicKey()
	if err != nil {
		return nil, err
	}

	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("jwks: signature base64: %w", err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sigBytes); err != nil {
		return nil, fmt.Errorf("jwks: signature verification failed")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("jwks: payload base64: %w", err)
	}
	return payloadBytes, nil
}
