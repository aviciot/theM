package dal

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Gateway policy admin endpoints keep their flat REST shape; storage is
// them.governance_policies (scope 'tenant' and 'profile'). See
// docs/GOVERNANCE_POLICY_DESIGN.md.

const tsFmt = `'YYYY-MM-DD"T"HH24:MI:SS"Z"'`

// GatewayPolicyRow is the admin-facing tenant policy representation.
type GatewayPolicyRow struct {
	TenantID            string          `json:"tenant_id"`
	AllowedModels       []string        `json:"allowed_models"`
	ModelAliases        json.RawMessage `json:"model_aliases"`
	MaxTokensPerRequest *int            `json:"max_tokens_per_request,omitempty"`
	MonthlyBudgetUSD    *float64        `json:"monthly_budget_usd,omitempty"`
	UpdatedAt           string          `json:"updated_at"`
}

// GatewayPolicyProfile is a named, reusable policy that can be assigned to clients.
type GatewayPolicyProfile struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenant_id"`
	Name                string          `json:"name"`
	AllowedModels       []string        `json:"allowed_models"`
	ModelAliases        json.RawMessage `json:"model_aliases"`
	MaxTokensPerRequest *int            `json:"max_tokens_per_request,omitempty"`
	MonthlyBudgetUSD    *float64        `json:"monthly_budget_usd,omitempty"`
	UpdatedAt           string          `json:"updated_at"`
}

func (p *GatewayPolicyProfile) fill(doc []byte) error {
	d, err := parseDoc(doc)
	if err != nil {
		return err
	}
	lf := toLegacy(d)
	p.AllowedModels, p.ModelAliases = lf.AllowedModels, lf.ModelAliases
	p.MaxTokensPerRequest, p.MonthlyBudgetUSD = lf.MaxTokensPerRequest, lf.MonthlyBudgetUSD
	return nil
}

// ── Tenant policy ─────────────────────────────────────────────────────────────

// GetGatewayPolicy returns the tenant-scope policy, or an empty one if none exists.
func (db *DB) GetGatewayPolicy(ctx context.Context, tenantID string) (GatewayPolicyRow, error) {
	const q = `
		SELECT doc, to_char(updated_at AT TIME ZONE 'UTC', ` + tsFmt + `)
		FROM them.governance_policies
		WHERE tenant_id = $1::uuid AND scope_type = 'tenant' AND scope_id = $1::text`

	p := GatewayPolicyRow{TenantID: tenantID}
	var doc []byte
	err := db.q.QueryRow(ctx, q, tenantID).Scan(&doc, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		doc = nil
	} else if err != nil {
		return GatewayPolicyRow{}, err
	}
	d, err := parseDoc(doc)
	if err != nil {
		return GatewayPolicyRow{}, err
	}
	lf := toLegacy(d)
	p.AllowedModels, p.ModelAliases = lf.AllowedModels, lf.ModelAliases
	p.MaxTokensPerRequest, p.MonthlyBudgetUSD = lf.MaxTokensPerRequest, lf.MonthlyBudgetUSD
	return p, nil
}

// UpsertGatewayPolicy writes the legacy fields into the tenant-scope doc, preserving
// anything else already in it (logging, extra budgets). Returns ErrInvalidPolicy on bad input.
func (db *DB) UpsertGatewayPolicy(ctx context.Context, tenantID string, allowedModels []string, modelAliases json.RawMessage, maxTokens *int, monthlyBudget *float64) error {
	var existing []byte
	err := db.q.QueryRow(ctx, `
		SELECT doc FROM them.governance_policies
		WHERE tenant_id = $1::uuid AND scope_type = 'tenant' AND scope_id = $1::text`, tenantID).Scan(&existing)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	doc, err := mergeLegacy(existing, allowedModels, modelAliases, maxTokens, monthlyBudget)
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO them.governance_policies (tenant_id, scope_type, scope_id, doc, updated_at)
		VALUES ($1::uuid, 'tenant', $1::text, $2::jsonb, now())
		ON CONFLICT (tenant_id, scope_type, scope_id) DO UPDATE SET
			doc = EXCLUDED.doc, version = governance_policies.version + 1, updated_at = now()`
	return db.q.Exec(ctx, q, tenantID, string(doc))
}

func mergeLegacy(existing []byte, allowed []string, aliases json.RawMessage, maxTokens *int, budget *float64) ([]byte, error) {
	base, err := parseDoc(existing)
	if err != nil {
		return nil, err
	}
	merged, err := applyLegacy(base, legacyFields{allowed, aliases, maxTokens, budget})
	if err != nil {
		return nil, err
	}
	return json.Marshal(merged)
}

// ── Policy profiles ───────────────────────────────────────────────────────────

const profileCols = `
		id::text, tenant_id::text, name, doc,
		to_char(updated_at AT TIME ZONE 'UTC', ` + tsFmt + `)`

// ListGatewayPolicyProfiles returns all policy profiles for a tenant.
func (db *DB) ListGatewayPolicyProfiles(ctx context.Context, tenantID string) ([]GatewayPolicyProfile, error) {
	rows, err := db.q.Query(ctx, `SELECT `+profileCols+`
		FROM them.governance_policies
		WHERE tenant_id = $1::uuid AND scope_type = 'profile'
		ORDER BY name, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []GatewayPolicyProfile{}
	for rows.Next() {
		var p GatewayPolicyProfile
		var doc []byte
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &doc, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := p.fill(doc); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// GetGatewayPolicyProfile returns one profile. Returns pgx.ErrNoRows when not found
// (including a malformed id).
func (db *DB) GetGatewayPolicyProfile(ctx context.Context, tenantID, id string) (GatewayPolicyProfile, error) {
	if _, err := uuid.Parse(id); err != nil {
		return GatewayPolicyProfile{}, pgx.ErrNoRows
	}
	var p GatewayPolicyProfile
	var doc []byte
	err := db.q.QueryRow(ctx, `SELECT `+profileCols+`
		FROM them.governance_policies
		WHERE tenant_id = $1::uuid AND scope_type = 'profile' AND id = $2::uuid`, tenantID, id).
		Scan(&p.ID, &p.TenantID, &p.Name, &doc, &p.UpdatedAt)
	if err != nil {
		return GatewayPolicyProfile{}, err
	}
	return p, p.fill(doc)
}

// CreateGatewayPolicyProfile inserts a new profile. Returns ErrInvalidPolicy on bad input;
// a duplicate name is a unique violation (see IsUniqueViolation).
func (db *DB) CreateGatewayPolicyProfile(ctx context.Context, tenantID, name string, allowedModels []string, modelAliases json.RawMessage, maxTokens *int, monthlyBudget *float64) (GatewayPolicyProfile, error) {
	doc, err := mergeLegacy(nil, allowedModels, modelAliases, maxTokens, monthlyBudget)
	if err != nil {
		return GatewayPolicyProfile{}, err
	}
	const q = `
		WITH n AS (SELECT gen_random_uuid() AS id)
		INSERT INTO them.governance_policies (id, tenant_id, scope_type, scope_id, name, doc)
		SELECT n.id, $1::uuid, 'profile', n.id::text, $2, $3::jsonb FROM n
		RETURNING id::text, tenant_id::text, name, doc,
		          to_char(updated_at AT TIME ZONE 'UTC', ` + tsFmt + `)`
	var p GatewayPolicyProfile
	var out []byte
	if err := db.q.QueryRow(ctx, q, tenantID, name, string(doc)).Scan(&p.ID, &p.TenantID, &p.Name, &out, &p.UpdatedAt); err != nil {
		return GatewayPolicyProfile{}, err
	}
	return p, p.fill(out)
}

// UpdateGatewayPolicyProfile updates a profile, preserving doc content the legacy
// fields cannot express. Returns pgx.ErrNoRows when the profile does not exist.
func (db *DB) UpdateGatewayPolicyProfile(ctx context.Context, tenantID, id, name string, allowedModels []string, modelAliases json.RawMessage, maxTokens *int, monthlyBudget *float64) error {
	existing, err := db.GetGatewayPolicyProfileDoc(ctx, tenantID, id)
	if err != nil {
		return err
	}
	doc, err := mergeLegacy(existing, allowedModels, modelAliases, maxTokens, monthlyBudget)
	if err != nil {
		return err
	}
	return db.q.Exec(ctx, `
		UPDATE them.governance_policies
		SET name = $3, doc = $4::jsonb, version = version + 1, updated_at = now()
		WHERE tenant_id = $1::uuid AND scope_type = 'profile' AND id = $2::uuid`,
		tenantID, id, name, string(doc))
}

// GetGatewayPolicyProfileDoc returns the raw doc of a profile (pgx.ErrNoRows if absent).
func (db *DB) GetGatewayPolicyProfileDoc(ctx context.Context, tenantID, id string) ([]byte, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, pgx.ErrNoRows
	}
	var doc []byte
	err := db.q.QueryRow(ctx, `
		SELECT doc FROM them.governance_policies
		WHERE tenant_id = $1::uuid AND scope_type = 'profile' AND id = $2::uuid`, tenantID, id).Scan(&doc)
	return doc, err
}

// DeleteGatewayPolicyProfile removes a profile; clients referencing it fall back to
// no profile (gateway_clients.governance_profile_id is ON DELETE SET NULL).
func (db *DB) DeleteGatewayPolicyProfile(ctx context.Context, tenantID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return nil
	}
	return db.q.Exec(ctx, `
		DELETE FROM them.governance_policies
		WHERE tenant_id = $1::uuid AND scope_type = 'profile' AND id = $2::uuid`, tenantID, id)
}

// SetGatewayClientPolicyProfile assigns (or clears) a policy profile on a client.
// The profile must belong to the same tenant; otherwise nothing is assigned
// (profileID is rejected as ErrInvalidPolicy).
func (db *DB) SetGatewayClientPolicyProfile(ctx context.Context, tenantID, clientID string, profileID *string) error {
	if profileID != nil {
		if _, err := db.GetGatewayPolicyProfileDoc(ctx, tenantID, *profileID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidPolicy
			}
			return err
		}
	}
	return db.q.Exec(ctx, `
		UPDATE them.gateway_clients
		SET governance_profile_id = $3::uuid
		WHERE tenant_id = $1::uuid AND id = $2::uuid`, tenantID, clientID, profileID)
}
