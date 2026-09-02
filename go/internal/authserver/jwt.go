package authserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors returned by token verification.
var (
	ErrTokenExpired   = errors.New("authserver: token expired")
	ErrTokenMalformed = errors.New("authserver: token malformed")
	ErrTokenSignature = errors.New("authserver: token signature invalid")
	ErrWrongTokenType = errors.New("authserver: wrong token type")
)

// accessClaims is the payload of an access token. Field names and shape MUST
// stay compatible with:
//   - Go bridge internal/auth.ValidateHS256JWT (reads sub/username/name/role/tenant_id/exp/iat)
type accessClaims struct {
	Sub         string   `json:"sub"`
	Username    string   `json:"username"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	TenantID    string   `json:"tenant_id"`
	Permissions []string `json:"permissions"`
	Exp         int64    `json:"exp"`
	Iat         int64    `json:"iat"`
	Type        string   `json:"type"`
}

// refreshClaims is the payload of a refresh token.
type refreshClaims struct {
	Sub  string `json:"sub"`
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
	Type string `json:"type"`
}

// jwtHeader is the fixed HS256 header.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// verifiedClaims is the normalised, type-agnostic view returned after a token is
// verified. Callers inspect Type to decide what is allowed.
type verifiedClaims struct {
	Sub      string
	Username string
	Name     string
	Role     string
	Exp      int64
	Iat      int64
	Type     string
}

// UserID parses Sub as an int64. Returns 0 on failure.
func (c verifiedClaims) UserID() int64 {
	id, _ := strconv.ParseInt(c.Sub, 10, 64)
	return id
}

// tokenSigner issues and verifies HS256 tokens with a fixed secret. All time
// values are Unix seconds so the wire shape matches PyJWT's default int encoding.
type tokenSigner struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
	now           func() time.Time // injectable for tests
}

// NewTokenSigner builds a tokenSigner from the service config. Exported so
// cmd/auth-server can wire OIDCHandlers without duplicating config parsing.
func NewTokenSigner(cfg *Config) *tokenSigner {
	return newTokenSigner([]byte(cfg.JWTSecret), cfg.AccessTokenExpiry, cfg.RefreshTokenExpiry)
}

func newTokenSigner(secret []byte, accessExpirySec, refreshExpirySec int) *tokenSigner {
	return &tokenSigner{
		secret:        secret,
		accessExpiry:  time.Duration(accessExpirySec) * time.Second,
		refreshExpiry: time.Duration(refreshExpirySec) * time.Second,
		now:           time.Now,
	}
}

// IssueAccessToken mints a signed access token. expirySec overrides the default
// access expiry when > 0 (used to honour roles.token_expiry). tenantID must be
// non-empty — the bridge rejects tokens without a tenant_id claim.
func (s *tokenSigner) IssueAccessToken(userID int64, username, name, role, tenantID string, expirySec int) (string, int, error) {
	now := s.now().UTC()
	ttl := s.accessExpiry
	if expirySec > 0 {
		ttl = time.Duration(expirySec) * time.Second
	}
	claims := accessClaims{
		Sub:         strconv.FormatInt(userID, 10),
		Username:    username,
		Name:        name,
		Role:        role,
		TenantID:    tenantID,
		Permissions: []string{},
		Exp:         now.Add(ttl).Unix(),
		Iat:         now.Unix(),
		Type:        "access",
	}
	tok, err := s.sign(claims)
	if err != nil {
		return "", 0, err
	}
	return tok, int(ttl.Seconds()), nil
}

// IssueRefreshToken mints a signed refresh token.
func (s *tokenSigner) IssueRefreshToken(userID int64) (string, error) {
	now := s.now().UTC()
	claims := refreshClaims{
		Sub:  strconv.FormatInt(userID, 10),
		Exp:  now.Add(s.refreshExpiry).Unix(),
		Iat:  now.Unix(),
		Type: "refresh",
	}
	return s.sign(claims)
}

// sign serialises claims and produces a compact HS256 JWT.
func (s *tokenSigner) sign(claims any) (string, error) {
	headerJSON, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", fmt.Errorf("authserver: marshal header: %w", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("authserver: marshal claims: %w", err)
	}
	headerEnc := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerEnc + "." + payloadEnc

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig, nil
}

// Verify parses and verifies an HS256 token signature and expiry, returning the
// normalised claims. It does NOT check the blacklist (that is the store's job).
func (s *tokenSigner) Verify(tokenString string) (*verifiedClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrTokenMalformed, len(parts))
	}

	headerBytes, err := base64urlDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header base64: %w", ErrTokenMalformed, err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: header JSON: %w", ErrTokenMalformed, err)
	}
	if !strings.EqualFold(header.Alg, "HS256") {
		return nil, fmt.Errorf("%w: unsupported algorithm %q (expected HS256)", ErrTokenMalformed, header.Alg)
	}

	sigBytes, err := base64urlDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature base64: %w", ErrTokenMalformed, err)
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sigBytes, mac.Sum(nil)) {
		return nil, ErrTokenSignature
	}

	payloadBytes, err := base64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload base64: %w", ErrTokenMalformed, err)
	}
	// Access and refresh share sub/exp/iat/type; unmarshal into the superset.
	var raw accessClaims
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("%w: payload JSON: %w", ErrTokenMalformed, err)
	}

	if raw.Exp > 0 && s.now().Unix() > raw.Exp {
		return nil, ErrTokenExpired
	}

	return &verifiedClaims{
		Sub:      raw.Sub,
		Username: raw.Username,
		Name:     raw.Name,
		Role:     raw.Role,
		Exp:      raw.Exp,
		Iat:      raw.Iat,
		Type:     raw.Type,
	}, nil
}

// base64urlDecode decodes unpadded base64url (RFC 7515).
func base64urlDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// hashToken returns the lowercase hex SHA-256 of a token, matching the Python
// utils/hashing.hash_token used to key user_sessions and blacklisted_tokens.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}
