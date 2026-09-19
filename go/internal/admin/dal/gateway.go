package dal

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ── Gateway client ────────────────────────────────────────────────────────────

// GatewayClient is one closed-agent registry entry.
type GatewayClient struct {
	ID        string  `json:"id"`
	TenantID  string  `json:"tenant_id"`
	TokenHash string  `json:"token_hash"`
	Label     string  `json:"label"`
	ProfileID *string `json:"profile_id,omitempty"`
	LastSeen  *string `json:"last_seen,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// ListGatewayClients returns all clients for a tenant.
func (db *DB) ListGatewayClients(ctx context.Context, tenantID string) ([]GatewayClient, error) {
	const q = `
		SELECT id::text, tenant_id::text, token_hash, label,
		       profile_id::text,
		       to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM them.gateway_clients
		WHERE tenant_id = $1::uuid
		ORDER BY created_at DESC`

	rows, err := db.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GatewayClient
	for rows.Next() {
		var c GatewayClient
		if err := rows.Scan(&c.ID, &c.TenantID, &c.TokenHash, &c.Label,
			&c.ProfileID, &c.LastSeen, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []GatewayClient{}
	}
	return out, nil
}

// CreateGatewayClient inserts a new client row and returns the created record.
func (db *DB) CreateGatewayClient(ctx context.Context, tenantID, tokenHash, label string) (GatewayClient, error) {
	const q = `
		INSERT INTO them.gateway_clients (tenant_id, token_hash, label)
		VALUES ($1::uuid, $2, $3)
		RETURNING id::text, tenant_id::text, token_hash, label,
		          profile_id::text,
		          to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		          to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

	var c GatewayClient
	err := db.q.QueryRow(ctx, q, tenantID, tokenHash, label).Scan(
		&c.ID, &c.TenantID, &c.TokenHash, &c.Label,
		&c.ProfileID, &c.LastSeen, &c.CreatedAt,
	)
	return c, err
}

// GetGatewayClient returns one client by ID within a tenant.
// Returns pgx.ErrNoRows when not found.
func (db *DB) GetGatewayClient(ctx context.Context, tenantID, id string) (GatewayClient, error) {
	const q = `
		SELECT id::text, tenant_id::text, token_hash, label,
		       profile_id::text,
		       to_char(last_seen AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM them.gateway_clients
		WHERE tenant_id = $1::uuid AND id = $2::uuid`

	var c GatewayClient
	err := db.q.QueryRow(ctx, q, tenantID, id).Scan(
		&c.ID, &c.TenantID, &c.TokenHash, &c.Label,
		&c.ProfileID, &c.LastSeen, &c.CreatedAt,
	)
	return c, err
}

// SetGatewayClientProfile assigns (or clears when profileID is nil) a profile on a client.
func (db *DB) SetGatewayClientProfile(ctx context.Context, tenantID, clientID string, profileID *string) error {
	const q = `
		UPDATE them.gateway_clients
		SET profile_id = $3::uuid
		WHERE tenant_id = $1::uuid AND id = $2::uuid`
	return db.q.Exec(ctx, q, tenantID, clientID, profileID)
}

// DeleteGatewayClient removes a client row.
func (db *DB) DeleteGatewayClient(ctx context.Context, tenantID, id string) error {
	const q = `DELETE FROM them.gateway_clients WHERE tenant_id = $1::uuid AND id = $2::uuid`
	return db.q.Exec(ctx, q, tenantID, id)
}

// ── Gateway profile ───────────────────────────────────────────────────────────

// GatewayProfile is a named policy bundle.
type GatewayProfile struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// GatewayProfileStep is one component in a profile pipeline.
type GatewayProfileStep struct {
	ID        string          `json:"id"`
	ProfileID string          `json:"profile_id"`
	DefID     string          `json:"def_id"`
	DefName   string          `json:"def_name,omitempty"`
	Position  int             `json:"position"`
	Config    json.RawMessage `json:"config"`
}

// ListGatewayProfiles returns all profiles for a tenant.
func (db *DB) ListGatewayProfiles(ctx context.Context, tenantID string) ([]GatewayProfile, error) {
	const q = `
		SELECT id::text, tenant_id::text, name, enabled,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM them.gateway_profiles
		WHERE tenant_id = $1::uuid
		ORDER BY created_at`

	rows, err := db.q.Query(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GatewayProfile
	for rows.Next() {
		var p GatewayProfile
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.Enabled, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []GatewayProfile{}
	}
	return out, nil
}

// CreateGatewayProfile inserts a new profile.
// Returns a unique-violation error (SQLSTATE 23505) when name already exists for the tenant.
func (db *DB) CreateGatewayProfile(ctx context.Context, tenantID, name string) (GatewayProfile, error) {
	const q = `
		INSERT INTO them.gateway_profiles (tenant_id, name)
		VALUES ($1::uuid, $2)
		RETURNING id::text, tenant_id::text, name, enabled,
		          to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

	var p GatewayProfile
	err := db.q.QueryRow(ctx, q, tenantID, name).Scan(
		&p.ID, &p.TenantID, &p.Name, &p.Enabled, &p.CreatedAt,
	)
	return p, err
}

// DeleteGatewayProfile removes a profile (cascades to steps via FK).
func (db *DB) DeleteGatewayProfile(ctx context.Context, tenantID, id string) error {
	const q = `DELETE FROM them.gateway_profiles WHERE tenant_id = $1::uuid AND id = $2::uuid`
	return db.q.Exec(ctx, q, tenantID, id)
}

// ListGatewayProfileSteps returns ordered steps for a profile, joined with def display_name.
func (db *DB) ListGatewayProfileSteps(ctx context.Context, profileID string) ([]GatewayProfileStep, error) {
	const q = `
		SELECT s.id::text, s.profile_id::text, s.def_id::text,
		       COALESCE(m.display_name, ''),
		       s.position, s.config
		FROM them.gateway_profile_steps s
		LEFT JOIN them.middleware_defs m ON m.id = s.def_id
		WHERE s.profile_id = $1::uuid
		ORDER BY s.position`

	rows, err := db.q.Query(ctx, q, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GatewayProfileStep
	for rows.Next() {
		var s GatewayProfileStep
		var cfg []byte
		if err := rows.Scan(&s.ID, &s.ProfileID, &s.DefID, &s.DefName, &s.Position, &cfg); err != nil {
			return nil, err
		}
		if len(cfg) > 0 {
			s.Config = json.RawMessage(cfg)
		} else {
			s.Config = json.RawMessage("{}")
		}
		out = append(out, s)
	}
	if out == nil {
		out = []GatewayProfileStep{}
	}
	return out, nil
}

// AddGatewayProfileStep inserts a step at the given position.
func (db *DB) AddGatewayProfileStep(ctx context.Context, profileID, defID string, position int, config json.RawMessage) (GatewayProfileStep, error) {
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	const q = `
		INSERT INTO them.gateway_profile_steps (profile_id, def_id, position, config)
		VALUES ($1::uuid, $2::uuid, $3, $4)
		RETURNING id::text, profile_id::text, def_id::text, position, config`

	var s GatewayProfileStep
	var cfg []byte
	err := db.q.QueryRow(ctx, q, profileID, defID, position, config).Scan(
		&s.ID, &s.ProfileID, &s.DefID, &s.Position, &cfg,
	)
	if err != nil {
		return GatewayProfileStep{}, err
	}
	if len(cfg) > 0 {
		s.Config = json.RawMessage(cfg)
	} else {
		s.Config = json.RawMessage("{}")
	}
	return s, nil
}

// DeleteGatewayProfileStep removes a step by ID.
func (db *DB) DeleteGatewayProfileStep(ctx context.Context, stepID string) error {
	const q = `DELETE FROM them.gateway_profile_steps WHERE id = $1::uuid`
	return db.q.Exec(ctx, q, stepID)
}

// ── Gateway policy ────────────────────────────────────────────────────────────

// GatewayPolicyRow is the admin-facing policy representation.
type GatewayPolicyRow struct {
	TenantID            string          `json:"tenant_id"`
	AllowedModels       []string        `json:"allowed_models"`
	ModelAliases        json.RawMessage `json:"model_aliases"`
	MaxTokensPerRequest *int            `json:"max_tokens_per_request,omitempty"`
	MonthlyBudgetUSD    *float64        `json:"monthly_budget_usd,omitempty"`
	UpdatedAt           string          `json:"updated_at"`
}

// GetGatewayPolicy returns the policy row for a tenant, or a zero row if none exists.
func (db *DB) GetGatewayPolicy(ctx context.Context, tenantID string) (GatewayPolicyRow, error) {
	const q = `
		SELECT tenant_id::text, COALESCE(allowed_models, '{}'), model_aliases,
		       max_tokens_per_request, monthly_budget_usd,
		       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM them.gateway_policies
		WHERE tenant_id = $1::uuid`

	var p GatewayPolicyRow
	var aliases []byte
	err := db.q.QueryRow(ctx, q, tenantID).Scan(
		&p.TenantID, &p.AllowedModels, &aliases,
		&p.MaxTokensPerRequest, &p.MonthlyBudgetUSD, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		p.TenantID = tenantID
		p.AllowedModels = []string{}
		p.ModelAliases = json.RawMessage("{}")
		return p, nil
	}
	if err != nil {
		return GatewayPolicyRow{}, err
	}
	if len(aliases) > 0 {
		p.ModelAliases = json.RawMessage(aliases)
	} else {
		p.ModelAliases = json.RawMessage("{}")
	}
	return p, nil
}

// UpsertGatewayPolicy inserts or updates the policy row for a tenant.
func (db *DB) UpsertGatewayPolicy(ctx context.Context, tenantID string, allowedModels []string, modelAliases json.RawMessage, maxTokens *int, monthlyBudget *float64) error {
	if len(modelAliases) == 0 {
		modelAliases = json.RawMessage("{}")
	}
	if allowedModels == nil {
		allowedModels = []string{}
	}
	const q = `
		INSERT INTO them.gateway_policies
			(tenant_id, allowed_models, model_aliases, max_tokens_per_request, monthly_budget_usd, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, now())
		ON CONFLICT (tenant_id) DO UPDATE SET
			allowed_models         = EXCLUDED.allowed_models,
			model_aliases          = EXCLUDED.model_aliases,
			max_tokens_per_request = EXCLUDED.max_tokens_per_request,
			monthly_budget_usd     = EXCLUDED.monthly_budget_usd,
			updated_at             = now()`
	return db.q.Exec(ctx, q, tenantID, allowedModels, modelAliases, maxTokens, monthlyBudget)
}

// ── Gateway requests (read-only for admin UI) ─────────────────────────────────

// GatewayRequestRow is one row from the gateway_requests log.
type GatewayRequestRow struct {
	ID             string  `json:"id"`
	ClientID       *string `json:"client_id,omitempty"`
	ClientLabel    *string `json:"client_label,omitempty"`
	Provider       *string `json:"provider,omitempty"`
	ModelRequested *string `json:"model_requested,omitempty"`
	ModelServed    *string `json:"model_served,omitempty"`
	Status         string  `json:"status"`
	HTTPStatus     *int    `json:"http_status,omitempty"`
	TokensIn       int     `json:"tokens_in"`
	TokensOut      int     `json:"tokens_out"`
	CostUSD        float64 `json:"cost_usd"`
	LatencyMS      *int    `json:"latency_ms,omitempty"`
	Streamed       bool    `json:"streamed"`
	CreatedAt      string  `json:"created_at"`
}

// ListGatewayRequests returns the most recent requests for a tenant (up to limit, max 500).
func (db *DB) ListGatewayRequests(ctx context.Context, tenantID string, limit int) ([]GatewayRequestRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT r.id::text,
		       r.client_id::text,
		       c.label,
		       r.provider, r.model_requested, r.model_served,
		       r.status, r.http_status,
		       r.tokens_in, r.tokens_out, r.cost_usd,
		       r.latency_ms, r.streamed,
		       to_char(r.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM them.gateway_requests r
		LEFT JOIN them.gateway_clients c ON c.id = r.client_id
		WHERE r.tenant_id = $1::uuid
		ORDER BY r.created_at DESC
		LIMIT $2`

	rows, err := db.q.Query(ctx, q, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GatewayRequestRow
	for rows.Next() {
		var row GatewayRequestRow
		if err := rows.Scan(
			&row.ID, &row.ClientID, &row.ClientLabel,
			&row.Provider, &row.ModelRequested, &row.ModelServed,
			&row.Status, &row.HTTPStatus,
			&row.TokensIn, &row.TokensOut, &row.CostUSD,
			&row.LatencyMS, &row.Streamed, &row.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if out == nil {
		out = []GatewayRequestRow{}
	}
	return out, nil
}
