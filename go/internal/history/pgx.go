// Package history provides persistent conversation history backed by PostgreSQL.
// Messages are stored via the them.task_messages + them.tasks schema. Because
// task_messages.role has a CHECK constraint of ('user','agent','system'), every
// domain.Message role is mapped to one of those three DB values and the true
// canonical role is preserved in a JSONB envelope inside the parts column.
package history

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/domain"
)

// dbRole is the set of values permitted by the task_messages.role CHECK constraint.
const (
	dbRoleUser   = "user"
	dbRoleAgent  = "agent"
	dbRoleSystem = "system"
)

// envelope is the JSONB structure stored in task_messages.parts.
// canonical_role preserves the domain role for lossless round-trip.
// summary=true marks a condensed history summary message.
type envelope struct {
	CanonicalRole string               `json:"canonical_role"`
	Summary       bool                 `json:"summary,omitempty"`
	Parts         []domain.ContentPart `json:"parts"`
}

// canonicalToDBRole maps domain.Message roles to the DB CHECK constraint values.
// assistant → agent, tool → agent, system → system, user → user.
func canonicalToDBRole(role string) string {
	switch role {
	case domain.RoleAssistant, domain.RoleTool:
		return dbRoleAgent
	case domain.RoleSystem:
		return dbRoleSystem
	default:
		return dbRoleUser
	}
}

// dbToCanonicalRole reads canonical_role from the envelope. Falls back to the
// db role if the envelope is absent (handles rows written before this code).
func dbToCanonicalRole(dbRole, canonicalRole string) string {
	if canonicalRole != "" {
		return canonicalRole
	}
	// Fallback: agent → assistant
	if dbRole == dbRoleAgent {
		return domain.RoleAssistant
	}
	return dbRole
}

// Store is the concrete history implementation backed by pgxpool.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewStore creates a history Store.
func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{pool: pool, logger: logger}
}

// LoadHistory fetches the most recent `limit` messages for contextID and
// tenantID, in chronological order. tenantID="" skips the tenant filter
// (single-tenant / bootstrap use). The DB query orders by descending ID to
// apply the LIMIT, then Go reverses the slice.
func (s *Store) LoadHistory(ctx context.Context, contextID, tenantID string, limit int) ([]domain.Message, error) {
	const q = `
SELECT tm.role, tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
ORDER BY tm.id DESC
LIMIT $3`

	rows, err := s.pool.Query(ctx, q, contextID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("history: load: %w", err)
	}
	defer rows.Close()

	var msgs []domain.Message
	for rows.Next() {
		var (
			dbRole string
			raw    []byte
		)
		if err := rows.Scan(&dbRole, &raw); err != nil {
			return nil, fmt.Errorf("history: scan: %w", err)
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			// Malformed JSONB — skip silently, log warning.
			s.logger.Warn("history: malformed envelope — skipping row", "error", err)
			continue
		}
		role := dbToCanonicalRole(dbRole, env.CanonicalRole)
		msgs = append(msgs, domain.Message{Role: role, Parts: env.Parts})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: rows: %w", err)
	}

	// Reverse to chronological order (DB returned DESC).
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// WriteMessage persists a single message to task_messages.
// It resolves (or creates) the root task row for contextID+runID+tenantID first,
// then assigns the next sequence number.
func (s *Store) WriteMessage(ctx context.Context, contextID, runID, tenantID string, msg domain.Message) error {
	taskID, err := s.resolveRootTaskID(ctx, contextID, runID, tenantID)
	if err != nil {
		return fmt.Errorf("history: resolve task: %w", err)
	}

	seq, err := s.nextSeq(ctx, taskID)
	if err != nil {
		return fmt.Errorf("history: next seq: %w", err)
	}

	env := envelope{
		CanonicalRole: msg.Role,
		Parts:         msg.Parts,
	}
	partsJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("history: marshal envelope: %w", err)
	}

	dbRole := canonicalToDBRole(msg.Role)

	const insertQ = `
INSERT INTO them.task_messages (task_id, seq, role, parts)
VALUES ($1::uuid, $2, $3, $4::jsonb)
ON CONFLICT (task_id, seq) DO NOTHING`

	if _, err := s.pool.Exec(ctx, insertQ, taskID, seq, dbRole, partsJSON); err != nil {
		return fmt.Errorf("history: insert message: %w", err)
	}
	return nil
}

// LoadSummary returns the text of the most recent summary message for contextID.
// Returns "" with nil error when no summary exists.
func (s *Store) LoadSummary(ctx context.Context, contextID, tenantID string) (string, error) {
	const q = `
SELECT tm.parts
FROM them.task_messages tm
JOIN them.tasks t ON t.id = tm.task_id
WHERE t.context_id = $1::uuid
  AND ($2 = '' OR t.tenant_id = $2::uuid)
  AND tm.role = 'system'
  AND (tm.parts->>'summary')::boolean = true
ORDER BY tm.id DESC
LIMIT 1`

	row := s.pool.QueryRow(ctx, q, contextID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		// pgx returns pgx.ErrNoRows when no row — treat as "no summary".
		return "", nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("history: unmarshal summary envelope: %w", err)
	}
	for _, p := range env.Parts {
		if p.Type == "text" {
			return p.Text, nil
		}
	}
	return "", nil
}

// SaveSummary persists a summary as a system message.
func (s *Store) SaveSummary(ctx context.Context, contextID, runID, tenantID, summary string) error {
	msg := domain.Message{
		Role:  domain.RoleSystem,
		Parts: []domain.ContentPart{{Type: "text", Text: summary}},
	}
	taskID, err := s.resolveRootTaskID(ctx, contextID, runID, tenantID)
	if err != nil {
		return fmt.Errorf("history: save summary resolve task: %w", err)
	}
	seq, err := s.nextSeq(ctx, taskID)
	if err != nil {
		return fmt.Errorf("history: save summary next seq: %w", err)
	}
	env := envelope{
		CanonicalRole: domain.RoleSystem,
		Summary:       true,
		Parts:         msg.Parts,
	}
	partsJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("history: save summary marshal: %w", err)
	}
	const insertQ = `
INSERT INTO them.task_messages (task_id, seq, role, parts)
VALUES ($1::uuid, $2, $3, $4::jsonb)
ON CONFLICT (task_id, seq) DO NOTHING`
	if _, err := s.pool.Exec(ctx, insertQ, taskID, seq, dbRoleSystem, partsJSON); err != nil {
		return fmt.Errorf("history: save summary insert: %w", err)
	}
	return nil
}

// resolveRootTaskID finds or creates the root tasks row for (contextID, runID, tenantID).
// Uses a find-then-insert pattern to be idempotent — the row is created once per run.
// tenantID is included in the lookup to prevent cross-tenant root task reuse.
func (s *Store) resolveRootTaskID(ctx context.Context, contextID, runID, tenantID string) (string, error) {
	// Try to find an existing task first, scoped to tenantID when non-empty.
	const findQ = `
SELECT id::text
FROM them.tasks
WHERE context_id = $1::uuid
  AND (run_id = $2::uuid OR ($2 = '' AND run_id IS NULL))
  AND ($3 = '' OR tenant_id = $3::uuid)
LIMIT 1`
	row := s.pool.QueryRow(ctx, findQ, contextID, runID, tenantID)
	var id string
	if err := row.Scan(&id); err == nil {
		return id, nil
	}

	// Not found — insert. tenant_id and run_id may be empty (NULL).
	const insertQ = `
INSERT INTO them.tasks (context_id, run_id, tenant_id, state, kind)
VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, 'working', 'root')
ON CONFLICT DO NOTHING
RETURNING id::text`
	row = s.pool.QueryRow(ctx, insertQ, contextID, runID, tenantID)
	if err := row.Scan(&id); err != nil {
		// Race: another goroutine inserted first — re-query with tenant filter.
		row2 := s.pool.QueryRow(ctx, findQ, contextID, runID, tenantID)
		if err2 := row2.Scan(&id); err2 != nil {
			return "", fmt.Errorf("history: resolve task (re-query): %w", err2)
		}
	}
	return id, nil
}

// nextSeq returns the next sequence number for taskID (max+1, or 1 if empty).
func (s *Store) nextSeq(ctx context.Context, taskID string) (int, error) {
	const q = `SELECT COALESCE(MAX(seq), 0) + 1 FROM them.task_messages WHERE task_id = $1::uuid`
	row := s.pool.QueryRow(ctx, q, taskID)
	var seq int
	if err := row.Scan(&seq); err != nil {
		return 0, fmt.Errorf("history: next seq: %w", err)
	}
	return seq, nil
}
