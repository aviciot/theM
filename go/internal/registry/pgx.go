package registry

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned when a definition cannot be resolved.
var ErrNotFound = errors.New("component definition not found")

// ErrDisabled is returned when the resolved definition is disabled.
var ErrDisabled = errors.New("component definition is disabled")

// ErrDeprecated is returned when the resolved definition is deprecated.
var ErrDeprecated = errors.New("component definition is deprecated")

// DBQuerier is the minimal SQL interface the registry DAL needs.
type DBQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner
}


// SingleRowScanner matches pgx.Row.
type SingleRowScanner interface {
	Scan(dest ...any) error
}

// PgxQuerier is the concrete DAL backed by a real pgx connection.
type PgxQuerier struct {
	q DBQuerier
}

// NewPgxQuerier creates a PgxQuerier.
func NewPgxQuerier(q DBQuerier) *PgxQuerier { return &PgxQuerier{q: q} }

// resolveByRefSQL resolves a component by stable ref within the target tenant.
// Builtins (scope='builtin', tenant_id IS NULL) are accessible to all tenants.
// Tenant-scoped components (scope='tenant') must belong to the requesting tenant.
// Exact version match is required — no "latest" fallback.
const resolveByRefSQL = `
SELECT id::text, kind, namespace, name, version, display_name,
       COALESCE(description,''), implementation_type,
       configuration_schema, default_config, capabilities,
       input_schema, output_schema, credential_schema,
       scope, COALESCE(tenant_id::text,''), status, content_hash,
       enabled, created_at, published_at
FROM them.component_definitions
WHERE kind = $1 AND namespace = $2 AND name = $3 AND version = $4
  AND (scope = 'builtin' OR tenant_id = $5::uuid)
LIMIT 1`

// ResolveByRef resolves a ComponentDefinition by portable reference (kind, namespace, name, version)
// scoped to the target tenant. Builtins are accessible to all tenants.
func (q *PgxQuerier) ResolveByRef(ctx context.Context, ref DefinitionRef, tenantID string) (*ComponentDefinition, error) {
	row := q.q.QueryRow(ctx, resolveByRefSQL, string(ref.Kind), ref.Namespace, ref.Name, ref.Version, tenantID)
	return scanDefinition(row)
}

func scanDefinition(row SingleRowScanner) (*ComponentDefinition, error) {
	var d ComponentDefinition
	var configSchemaRaw, defaultConfigRaw, capabilitiesRaw []byte
	var inputSchemaRaw, outputSchemaRaw, credSchemaRaw []byte
	var publishedAt *time.Time

	err := row.Scan(
		&d.ID, &d.Kind, &d.Namespace, &d.Name, &d.Version,
		&d.DisplayName, &d.Description, &d.ImplementationType,
		&configSchemaRaw, &defaultConfigRaw, &capabilitiesRaw,
		&inputSchemaRaw, &outputSchemaRaw, &credSchemaRaw,
		&d.Scope, &d.TenantID, &d.Status, &d.ContentHash,
		&d.Enabled, &d.CreatedAt, &publishedAt,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	d.PublishedAt = publishedAt

	if err := json.Unmarshal(configSchemaRaw, &d.ConfigurationSchema); err != nil {
		d.ConfigurationSchema = map[string]any{}
	}
	if err := json.Unmarshal(defaultConfigRaw, &d.DefaultConfig); err != nil {
		d.DefaultConfig = map[string]any{}
	}
	if len(capabilitiesRaw) > 0 {
		_ = json.Unmarshal(capabilitiesRaw, &d.Capabilities)
	}
	if len(inputSchemaRaw) > 0 {
		_ = json.Unmarshal(inputSchemaRaw, &d.InputSchema)
	}
	if len(outputSchemaRaw) > 0 {
		_ = json.Unmarshal(outputSchemaRaw, &d.OutputSchema)
	}
	if len(credSchemaRaw) > 0 {
		_ = json.Unmarshal(credSchemaRaw, &d.CredentialSchema)
	}
	return &d, nil
}
