package execution

// Tests for AccessModeExternal (external_jwt) admit paths.
// Uses fake RuntimeIDPLoader and ExternalJWTValidator via jwks fake fetcher.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/jwks"
	"github.com/aviciot/them/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Test key ─────────────────────────────────────────────────────────────────

var extLifeKey *rsa.PrivateKey

func init() {
	var err error
	extLifeKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("lifecycle_external_jwt_test: " + err.Error())
	}
}

// ── Fakes ────────────────────────────────────────────────────────────────────

type fakeRIDPLoader struct {
	cfg *transport.RuntimeIDPConfig
	err error
}

func (f *fakeRIDPLoader) GetTenantRuntimeIDP(_ context.Context, _ string) (*transport.RuntimeIDPConfig, error) {
	return f.cfg, f.err
}

type fakeJWKSFetcherExt struct {
	doc *jwks.Document
}

func (f *fakeJWKSFetcherExt) FetchJWKS(_ context.Context, _ string) (*jwks.Document, error) {
	return f.doc, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func extJWKSDoc() *jwks.Document {
	pub := &extLifeKey.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	e := base64.RawURLEncoding.EncodeToString(eBytes)
	return &jwks.Document{Keys: []jwks.Key{{
		Kid: "k1", Kty: "RSA", Alg: "RS256", Use: "sig", N: n, E: e,
	}}}
}

func extJWTToken(claims map[string]any) string {
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
	pay, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString(hdr)
	p := base64.RawURLEncoding.EncodeToString(pay)
	signing := h + "." + p
	digest := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, extLifeKey, crypto.SHA256, digest[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func validExtClaims() map[string]any {
	return map[string]any{
		"sub": "bank-customer-42",
		"iss": "https://bank.example.com",
		"aud": "them-runtime",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
}

const bankTenantID = "tenant-id-1"

func externalEP() *epconfig.EPConfig {
	return &epconfig.EPConfig{
		EPID:              "ep-id-ext",
		AppID:             "app-id-ext",
		TenantID:          bankTenantID,
		EPSlug:            "ext-ep",
		AppEnabled:        true,
		EPEnabled:         true,
		EPType:            "websocket",
		AccessMode:        epconfig.AccessModeExternal,
		AllowedPrincipals: "external",
	}
}

func buildExtLifecycle(ridp *fakeRIDPLoader, validator *auth.ExternalJWTValidator) *Lifecycle {
	ep := externalEP()
	lc := buildLifecycle(ep, &fakeAuth{}, &fakeGate{}, &fakeSession{}, &fakeRecorder{}, nil)
	lc.WithExternalJWT(ridp, validator)
	return lc
}

func goodRIDPConfig() *transport.RuntimeIDPConfig {
	return &transport.RuntimeIDPConfig{
		JWKSUri:  "https://bank.example.com/.well-known/jwks.json",
		Issuer:   "https://bank.example.com",
		Audience: "them-runtime",
		SubClaim: "sub",
	}
}

// ── Tests ────────────────────────────────────────────────────────────────────

// LC-EXT-1: valid external JWT → admitted; ExternalUserID from sub claim.
func TestAccessModeExternal_ValidJWT_Admitted(t *testing.T) {
	cache := jwks.NewCache(&fakeJWKSFetcherExt{doc: extJWKSDoc()}, 0)
	validator := auth.NewExternalJWTValidator(cache)
	ridp := &fakeRIDPLoader{cfg: goodRIDPConfig()}
	lc := buildExtLifecycle(ridp, validator)

	token := extJWTToken(validExtClaims())
	req := ExecutionRequest{
		TenantID:    bankTenantID,
		EPSlug:      "ext-ep",
		AppSlug:     "app",
		RawToken:    token,
		UserMessage: domain.Message{Role: "user"},
	}
	h, err := lc.Admit(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "bank-customer-42", h.ExternalUserID, "ExternalUserID must come from JWT sub")
	assert.Empty(t, h.UserID, "UserID must be 0 for external JWT callers")
}

// LC-EXT-2: no token → 401.
func TestAccessModeExternal_NoToken_Rejected(t *testing.T) {
	cache := jwks.NewCache(&fakeJWKSFetcherExt{doc: extJWKSDoc()}, 0)
	validator := auth.NewExternalJWTValidator(cache)
	lc := buildExtLifecycle(&fakeRIDPLoader{cfg: goodRIDPConfig()}, validator)

	req := ExecutionRequest{TenantID: bankTenantID, EPSlug: "ext-ep", AppSlug: "app"}
	_, err := lc.Admit(context.Background(), req)
	require.Error(t, err)
	var ae *AdmitError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, AdmitErrUnauthorized, ae.Kind)
}

// LC-EXT-3: no JWKS validator configured → 401.
func TestAccessModeExternal_NoValidator_Rejected(t *testing.T) {
	ep := externalEP()
	lc := buildLifecycle(ep, &fakeAuth{}, &fakeGate{}, &fakeSession{}, &fakeRecorder{}, nil)
	// Deliberately NOT calling WithExternalJWT.

	token := extJWTToken(validExtClaims())
	req := ExecutionRequest{TenantID: bankTenantID, EPSlug: "ext-ep", AppSlug: "app", RawToken: token}
	_, err := lc.Admit(context.Background(), req)
	require.Error(t, err)
	var ae *AdmitError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, AdmitErrUnauthorized, ae.Kind)
}

// LC-EXT-4: no runtime IDP config for tenant → 401.
func TestAccessModeExternal_NoRIDPConfig_Rejected(t *testing.T) {
	cache := jwks.NewCache(&fakeJWKSFetcherExt{doc: extJWKSDoc()}, 0)
	validator := auth.NewExternalJWTValidator(cache)
	ridp := &fakeRIDPLoader{err: errors.New("not found")}
	lc := buildExtLifecycle(ridp, validator)

	token := extJWTToken(validExtClaims())
	req := ExecutionRequest{TenantID: bankTenantID, EPSlug: "ext-ep", AppSlug: "app", RawToken: token}
	_, err := lc.Admit(context.Background(), req)
	require.Error(t, err)
	var ae *AdmitError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, AdmitErrUnauthorized, ae.Kind)
}

// LC-EXT-5: tampered JWT → 401.
func TestAccessModeExternal_InvalidJWT_Rejected(t *testing.T) {
	cache := jwks.NewCache(&fakeJWKSFetcherExt{doc: extJWKSDoc()}, 0)
	validator := auth.NewExternalJWTValidator(cache)
	lc := buildExtLifecycle(&fakeRIDPLoader{cfg: goodRIDPConfig()}, validator)

	// Use a badly formed token.
	req := ExecutionRequest{
		TenantID: bankTenantID, EPSlug: "ext-ep", AppSlug: "app",
		RawToken: "not.a.jwt",
	}
	_, err := lc.Admit(context.Background(), req)
	require.Error(t, err)
	var ae *AdmitError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, AdmitErrUnauthorized, ae.Kind)
}

// LC-EXT-6: external_jwt EP with allowed_principals="internal" → blocked by CheckPrincipal.
func TestAccessModeExternal_PrincipalGuard_Blocked(t *testing.T) {
	cache := jwks.NewCache(&fakeJWKSFetcherExt{doc: extJWKSDoc()}, 0)
	validator := auth.NewExternalJWTValidator(cache)
	ridp := &fakeRIDPLoader{cfg: goodRIDPConfig()}

	ep := externalEP()
	ep.AllowedPrincipals = "internal" // external JWT caller is "external" → blocked
	lc := NewLifecycleWithRecorder(&fakeAuth{}, &fakeEPLoader{cfg: ep}, &fakeGate{}, &fakeSession{}, &fakeRecorder{}, nil, devLogger)
	lc.WithExternalJWT(ridp, validator)

	token := extJWTToken(validExtClaims())
	req := ExecutionRequest{
		TenantID: bankTenantID, EPSlug: "ext-ep", AppSlug: "app",
		RawToken: token,
	}
	_, err := lc.Admit(context.Background(), req)
	require.Error(t, err)
	var ae *AdmitError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, AdmitErrForbidden, ae.Kind)
}

// LC-EXT-7: X-External-User header is NOT honoured for external_jwt callers —
// ExternalUserID must come solely from the validated JWT sub.
// (header is simply not in ExecutionRequest for external_jwt — this test confirms
//  the sub from the JWT is what appears on the handle, not any caller-supplied value.)
func TestAccessModeExternal_HeaderIgnored_SubFromJWT(t *testing.T) {
	cache := jwks.NewCache(&fakeJWKSFetcherExt{doc: extJWKSDoc()}, 0)
	validator := auth.NewExternalJWTValidator(cache)
	lc := buildExtLifecycle(&fakeRIDPLoader{cfg: goodRIDPConfig()}, validator)

	token := extJWTToken(validExtClaims())
	req := ExecutionRequest{
		TenantID:       bankTenantID,
		EPSlug:         "ext-ep",
		AppSlug:        "app",
		RawToken:       token,
		ExternalUserID: "attacker-injected", // caller tries to override
		UserMessage:    domain.Message{Role: "user"},
	}
	h, err := lc.Admit(context.Background(), req)
	require.NoError(t, err)
	// The lifecycle overwrites ExternalUserID from the JWT sub, not from the request.
	assert.Equal(t, "bank-customer-42", h.ExternalUserID,
		"ExternalUserID must be from JWT sub, not from request field")
}
