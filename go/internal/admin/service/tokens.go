package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
)

// TokenGenerator issues an opaque bearer token and its SHA-256 hex storage hash.
// Generate returns (plaintext, sha256HexHash, err). The plaintext is shown once
// at creation time; the hash is what the DAL persists in them.access_tokens.token_hash.
type TokenGenerator interface {
	Generate(ctx context.Context) (plaintext string, hash string, err error)
}

// randTokenGenerator matches Python: secrets.token_urlsafe(32) + sha256 hex.
type randTokenGenerator struct{}

func (randTokenGenerator) Generate(_ context.Context) (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("token generate: %w", err)
	}
	// RawURLEncoding = no padding, URL-safe alphabet — matches token_urlsafe(32).
	plaintext := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, hex.EncodeToString(sum[:]), nil
}

// TokenService owns access-token CRUD business logic.
type TokenService struct {
	dal   Dal
	cache Cache
	gen   TokenGenerator
}

// NewTokenService creates a TokenService. Pass nil for gen to use the default
// crypto/rand generator (matches Python's secrets.token_urlsafe(32) + sha256).
func NewTokenService(d Dal, c Cache, g TokenGenerator) *TokenService {
	if g == nil {
		g = randTokenGenerator{}
	}
	return &TokenService{dal: d, cache: c, gen: g}
}

// List returns all tokens for the given tenant, optionally filtered by userID. Always returns []
// (never nil), ordered by created_at DESC.
func (s *TokenService) List(ctx context.Context, tenantID string, userID *int64) ([]dal.Token, error) {
	return s.dal.ListTokens(ctx, tenantID, userID)
}

// Get returns a single token scoped to the tenant. Any DAL error maps to ErrNotFound (matches
// Python's _get_or_404 pattern used by all resource getters).
func (s *TokenService) Get(ctx context.Context, tenantID, id string) (dal.Token, error) {
	t, err := s.dal.GetToken(ctx, tenantID, id)
	if err != nil {
		return dal.Token{}, ErrNotFound
	}
	return t, nil
}

// Create generates a new token, validates orchestrator existence if orchID is set (within tenant),
// and returns a TokenCreatedOut with the one-time plaintext included.
func (s *TokenService) Create(ctx context.Context, tenantID string, in dal.TokenCreateRow, orchID *string) (dal.TokenCreatedOut, error) {
	if orchID != nil && *orchID != "" {
		exists, err := s.dal.OrchestratorExists(ctx, tenantID, *orchID)
		if err != nil {
			return dal.TokenCreatedOut{}, fmt.Errorf("check orchestrator: %w", err)
		}
		if !exists {
			return dal.TokenCreatedOut{}, &FieldError{
				Kind:    ErrNotFound,
				Message: "Orchestrator " + *orchID + " not found",
			}
		}
		in.OrchestratorID = orchID
	}

	plaintext, hash, err := s.gen.Generate(ctx)
	if err != nil {
		return dal.TokenCreatedOut{}, fmt.Errorf("generate token: %w", err)
	}
	in.TokenHash = hash

	row, err := s.dal.CreateToken(ctx, tenantID, in)
	if err != nil {
		if dal.IsUniqueViolation(err) {
			return dal.TokenCreatedOut{}, unprocessable("token already exists (hash collision — retry)")
		}
		return dal.TokenCreatedOut{}, err
	}

	return dal.TokenCreatedOut{Token: row, Plaintext: plaintext}, nil
}

// Update applies a partial patch to a token scoped to the tenant, then invalidates the cache.
// Returns ErrNotFound when the token does not exist or belongs to another tenant.
func (s *TokenService) Update(ctx context.Context, tenantID, id string, patch dal.TokenPatchRow) (dal.Token, error) {
	hash, out, err := s.dal.UpdateToken(ctx, tenantID, id, patch)
	if err != nil {
		if dal.IsNoRows(err) {
			return dal.Token{}, ErrNotFound
		}
		return dal.Token{}, err
	}
	s.invalidate(ctx, hash)
	return out, nil
}

// Delete hard-deletes a token scoped to the tenant and invalidates the cache.
// Returns ErrNotFound when the token does not exist or belongs to another tenant.
func (s *TokenService) Delete(ctx context.Context, tenantID, id string) error {
	hash, err := s.dal.DeleteToken(ctx, tenantID, id)
	if err != nil {
		if dal.IsNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	s.invalidate(ctx, hash)
	return nil
}

// invalidate evicts the token from the L2 Redis cache and publishes a
// cross-pod L1 eviction message. Matches Python's invalidate_token(token_hash).
// Using the hash directly (not the raw token) because the admin path never
// holds the plaintext after creation.
func (s *TokenService) invalidate(ctx context.Context, hash string) {
	if s.cache == nil || hash == "" {
		return
	}
	_ = s.cache.Del(ctx, "them:session:token:"+hash)
	_ = s.cache.Publish(ctx, "them:token:revoked", hash)
}
