package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxQuerier implements TokenQuerier against them.access_tokens using pgx.
// It filters for enabled=true and unexpired rows; disabled or expired tokens
// return ErrTokenNotFound so callers handle them identically to missing tokens.
type PgxQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxQuerier wraps pool as a TokenQuerier for the token cache.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool}
}

// QueryToken looks up a token by its sha256-hex hash.
// Returns ErrTokenNotFound when no row matches, the token is disabled, or it
// has expired. The row's user_id is returned as TokenRow.ID; ApplicationID is
// always 0 because them.access_tokens has no application_id column.
func (q *PgxQuerier) QueryToken(ctx context.Context, hashHex string) (*TokenRow, error) {
	const sql = `
		SELECT user_id, created_at, expires_at
		FROM them.access_tokens
		WHERE token_hash = $1
		  AND enabled = true
		  AND (expires_at IS NULL OR expires_at > now())`

	var userID int64
	var createdAt time.Time
	var expiresAt *time.Time

	err := q.pool.QueryRow(ctx, sql, hashHex).Scan(&userID, &createdAt, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}

	return &TokenRow{
		ID:        userID,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}
