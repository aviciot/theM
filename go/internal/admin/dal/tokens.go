package dal

import (
	"context"
	"fmt"
	"time"
)

// tokenSelectCols is the column list for all token queries.
// token_hash is included last so scanToken can capture it for cache invalidation.
// Nullable timestamp columns are cast to text with COALESCE so we can detect NULL
// as an empty string, then convert to nil *string before returning.
const tokenSelectCols = `
	id::text,
	label,
	user_id,
	COALESCE(orchestrator_id::text, ''),
	enabled,
	COALESCE((expires_at AT TIME ZONE 'UTC')::text, ''),
	COALESCE((last_used_at AT TIME ZONE 'UTC')::text, ''),
	(created_at AT TIME ZONE 'UTC')::text,
	token_hash`

// scanToken scans one token row. Empty strings from COALESCE become nil *string.
// Timestamps are normalized to RFC3339 so JSON output matches Python's ISO8601.
func scanToken(row SingleRowScanner) (Token, error) {
	var (
		orchID     string
		expiresAt  string
		lastUsedAt string
		createdAt  string
		t          Token
	)
	if err := row.Scan(
		&t.ID, &t.Label, &t.UserID,
		&orchID, &t.Enabled,
		&expiresAt, &lastUsedAt, &createdAt,
		&t.TokenHash,
	); err != nil {
		return Token{}, err
	}
	if orchID != "" {
		t.OrchestratorID = &orchID
	}
	t.ExpiresAt = normTS(expiresAt)
	t.LastUsedAt = normTS(lastUsedAt)
	t.CreatedAt = parseTS(createdAt)
	return t, nil
}

// normTS converts a PG timestamptz text value to RFC3339 *string, or nil if empty.
func normTS(s string) *string {
	if s == "" {
		return nil
	}
	out := parseTS(s)
	return &out
}

// parseTS parses the PG ::text representation of a timestamptz and normalises
// it to RFC3339 so Go output matches Python's datetime.isoformat() output.
// Falls back to the raw string if parsing fails.
func parseTS(s string) string {
	if s == "" {
		return s
	}
	// PG outputs "2006-01-02 15:04:05+00" — try several formats.
	// AT TIME ZONE 'UTC' on timestamptz returns timestamp (no tz suffix), so
	// those layouts must be tried with time.UTC as the assumed location.
	utc := time.UTC
	type entry struct {
		layout string
		loc    *time.Location
	}
	for _, e := range []entry{
		{"2006-01-02T15:04:05Z07:00", utc},                      // already RFC3339
		{"2006-01-02 15:04:05.999999999Z07:00", utc},            // PG with microseconds + tz
		{"2006-01-02 15:04:05Z07:00", utc},                      // PG without micros + tz
		{"2006-01-02 15:04:05.999999999+00", utc},               // PG micros +00
		{"2006-01-02 15:04:05+00", utc},                         // PG explicit +00
		{"2006-01-02 15:04:05.999999999", utc},                  // AT TIME ZONE 'UTC' — no tz suffix
		{"2006-01-02 15:04:05", utc},                            // AT TIME ZONE 'UTC' — no micros
	} {
		if t, err := time.ParseInLocation(e.layout, s, e.loc); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s // best-effort fallback
}

// ── Token DAL methods ─────────────────────────────────────────────────────────

// ListTokens returns all access tokens, optionally filtered by user_id, ordered
// by created_at DESC. Never returns nil (empty slice on no rows).
func (db *DB) ListTokens(ctx context.Context, userID *int64) ([]Token, error) {
	var (
		rows RowScanner
		err  error
	)
	if userID != nil {
		rows, err = db.q.Query(ctx, fmt.Sprintf(`
			SELECT %s FROM them.access_tokens
			WHERE user_id = $1
			ORDER BY created_at DESC`, tokenSelectCols), *userID)
	} else {
		rows, err = db.q.Query(ctx, fmt.Sprintf(`
			SELECT %s FROM them.access_tokens
			ORDER BY created_at DESC`, tokenSelectCols))
	}
	if err != nil {
		return []Token{}, err
	}
	defer rows.Close() //nolint:errcheck

	var out []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []Token{}
	}
	return out, rows.Close()
}

// GetToken returns a single token by UUID. Returns pgx.ErrNoRows when not found.
func (db *DB) GetToken(ctx context.Context, id string) (Token, error) {
	row := db.q.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM them.access_tokens WHERE id = $1::uuid`, tokenSelectCols), id)
	return scanToken(row)
}

// OrchestratorExists returns true when an orchestrator with the given UUID exists.
func (db *DB) OrchestratorExists(ctx context.Context, orchID string) (bool, error) {
	var exists bool
	row := db.q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM them.orchestrators WHERE id = $1::uuid)`, orchID)
	return exists, row.Scan(&exists)
}

// CreateToken inserts a new access token row and returns the full scanned Token.
func (db *DB) CreateToken(ctx context.Context, in TokenCreateRow) (Token, error) {
	orchID := ""
	if in.OrchestratorID != nil {
		orchID = *in.OrchestratorID
	}
	expiresAt := ""
	if in.ExpiresAt != nil {
		expiresAt = *in.ExpiresAt
	}
	row := db.q.ExecReturning(ctx, fmt.Sprintf(`
		INSERT INTO them.access_tokens
			(token_hash, label, user_id, orchestrator_id, expires_at, enabled)
		VALUES (
			$1,
			$2,
			$3,
			NULLIF($4, '')::uuid,
			NULLIF($5, '')::timestamptz,
			true
		)
		RETURNING %s`, tokenSelectCols),
		in.TokenHash, in.Label, in.UserID, orchID, expiresAt)
	return scanToken(row)
}

// UpdateToken applies a partial update (only non-nil patch fields are changed)
// and returns the updated token hash (for cache invalidation) and the new row.
// Returns ("", Token{}, pgx.ErrNoRows) when not found.
func (db *DB) UpdateToken(ctx context.Context, id string, patch TokenPatchRow) (string, Token, error) {
	// Build a COALESCE-style update for each nullable patch field.
	// expires_at uses a CASE expression because we need to distinguish
	// "not provided" (nil) from "set to null" (explicit null string).
	expiresProvided := patch.ExpiresAt != nil
	expiresVal := ""
	if expiresProvided && patch.ExpiresAt != nil {
		expiresVal = *patch.ExpiresAt
	}
	row := db.q.ExecReturning(ctx, fmt.Sprintf(`
		UPDATE them.access_tokens SET
			label      = COALESCE($2, label),
			enabled    = COALESCE($3, enabled),
			expires_at = CASE WHEN $4 THEN NULLIF($5, '')::timestamptz ELSE expires_at END
		WHERE id = $1::uuid
		RETURNING %s`, tokenSelectCols),
		id, patch.Label, patch.Enabled, expiresProvided, expiresVal)
	t, err := scanToken(row)
	if err != nil {
		return "", Token{}, err
	}
	return t.TokenHash, t, nil
}

// DeleteToken hard-deletes a token row and returns its hash for cache invalidation.
// Returns ("", pgx.ErrNoRows) when not found.
func (db *DB) DeleteToken(ctx context.Context, id string) (string, error) {
	var hash string
	row := db.q.ExecReturning(ctx,
		`DELETE FROM them.access_tokens WHERE id = $1::uuid RETURNING token_hash`, id)
	return hash, row.Scan(&hash)
}
