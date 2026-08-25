package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Server is a row from them.mcp_servers.
type Server struct {
	ID                        string
	TenantID                  string
	Name                      string
	Slug                      string
	Description               string
	Transport                 string
	URL                       string
	AuthType                  string
	HealthStatus              string
	LastCheckedAt             *time.Time
	LastError                 string
	ToolsManifest             json.RawMessage
	Capabilities              json.RawMessage
	Enabled                   bool
	ProbeCredentialEncrypted  string // Fernet-encrypted; empty = no probe auth
}

// AppCredential is a row from them.app_mcp_credentials.
type AppCredential struct {
	CredentialEncrypted string
	AuthHeaderName      string
}

// DAL is the data-access layer for them-mcp-service.
// It only reads/writes them.mcp_servers and them.app_mcp_credentials.
type DAL struct {
	pool *pgxpool.Pool
}

// NewDAL creates a DAL backed by the given pool.
func NewDAL(pool *pgxpool.Pool) *DAL {
	return &DAL{pool: pool}
}

// ListEnabledServers returns all enabled mcp_servers rows across all tenants.
// The health loop uses this to know which servers to probe.
func (d *DAL) ListEnabledServers(ctx context.Context) ([]Server, error) {
	const q = `
		SELECT id::text, tenant_id::text, name, slug,
		       COALESCE(description,''), transport, COALESCE(url,''), auth_type,
		       health_status,
		       last_checked_at,
		       COALESCE(last_error,''),
		       tools_manifest, capabilities, enabled,
		       COALESCE(probe_credential_encrypted,'')
		FROM them.mcp_servers
		WHERE enabled = true
		ORDER BY created_at ASC`

	rows, err := d.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("dal: list enabled servers: %w", err)
	}
	defer rows.Close()

	var out []Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetServerByID returns a single server by UUID. Returns pgx.ErrNoRows when not found.
func (d *DAL) GetServerByID(ctx context.Context, id string) (Server, error) {
	const q = `
		SELECT id::text, tenant_id::text, name, slug,
		       COALESCE(description,''), transport, COALESCE(url,''), auth_type,
		       health_status,
		       last_checked_at,
		       COALESCE(last_error,''),
		       tools_manifest, capabilities, enabled,
		       COALESCE(probe_credential_encrypted,'')
		FROM them.mcp_servers
		WHERE id = $1::uuid`

	row := d.pool.QueryRow(ctx, q, id)
	return scanServer(row)
}

// GetServerBySlugAndTenant returns a server scoped to a tenant by slug.
// Returns pgx.ErrNoRows when not found or tenant mismatch.
func (d *DAL) GetServerBySlugAndTenant(ctx context.Context, slug, tenantID string) (Server, error) {
	const q = `
		SELECT id::text, tenant_id::text, name, slug,
		       COALESCE(description,''), transport, COALESCE(url,''), auth_type,
		       health_status,
		       last_checked_at,
		       COALESCE(last_error,''),
		       tools_manifest, capabilities, enabled,
		       COALESCE(probe_credential_encrypted,'')
		FROM them.mcp_servers
		WHERE slug = $1 AND tenant_id = $2::uuid AND enabled = true`

	row := d.pool.QueryRow(ctx, q, slug, tenantID)
	return scanServer(row)
}

// UpdateHealth writes health probe results back to the DB.
func (d *DAL) UpdateHealth(ctx context.Context, id, status, lastError string) error {
	const q = `
		UPDATE them.mcp_servers
		SET health_status   = $2,
		    last_checked_at = now(),
		    last_error      = NULLIF($3, ''),
		    updated_at      = now()
		WHERE id = $1::uuid`

	_, err := d.pool.Exec(ctx, q, id, status, lastError)
	if err != nil {
		return fmt.Errorf("dal: update health: %w", err)
	}
	return nil
}

// UpdateManifest writes a freshly discovered tools_manifest to the DB.
func (d *DAL) UpdateManifest(ctx context.Context, id string, manifest, capabilities json.RawMessage) error {
	const q = `
		UPDATE them.mcp_servers
		SET tools_manifest = $2::jsonb,
		    capabilities   = $3::jsonb,
		    updated_at     = now()
		WHERE id = $1::uuid`

	_, err := d.pool.Exec(ctx, q, id, manifest, capabilities)
	if err != nil {
		return fmt.Errorf("dal: update manifest: %w", err)
	}
	return nil
}

// GetCredential returns the encrypted credential for (applicationID, serverID).
// Returns pgx.ErrNoRows when no credential has been set.
func (d *DAL) GetCredential(ctx context.Context, applicationID, serverID string) (AppCredential, error) {
	const q = `
		SELECT COALESCE(credential_encrypted,''),
		       COALESCE(auth_header_name,'Authorization')
		FROM them.app_mcp_credentials
		WHERE application_id = $1::uuid AND mcp_server_id = $2::uuid`

	var c AppCredential
	err := d.pool.QueryRow(ctx, q, applicationID, serverID).Scan(
		&c.CredentialEncrypted, &c.AuthHeaderName,
	)
	if err != nil {
		return c, fmt.Errorf("dal: get credential: %w", err)
	}
	return c, nil
}

// GetApplicationTenantID returns the tenant_id for the given application UUID.
// Used by the executor to verify tenant isolation before any credential lookup.
func (d *DAL) GetApplicationTenantID(ctx context.Context, applicationID string) (string, error) {
	const q = `SELECT tenant_id::text FROM them.applications WHERE id = $1::uuid`
	var tid string
	if err := d.pool.QueryRow(ctx, q, applicationID).Scan(&tid); err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("dal: application %s not found", applicationID)
		}
		return "", fmt.Errorf("dal: get app tenant: %w", err)
	}
	return tid, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(r rowScanner) (Server, error) {
	var s Server
	var tm, caps []byte
	err := r.Scan(
		&s.ID, &s.TenantID, &s.Name, &s.Slug,
		&s.Description, &s.Transport, &s.URL, &s.AuthType,
		&s.HealthStatus, &s.LastCheckedAt, &s.LastError,
		&tm, &caps, &s.Enabled,
		&s.ProbeCredentialEncrypted,
	)
	if err != nil {
		return s, fmt.Errorf("dal: scan server: %w", err)
	}
	s.ToolsManifest = tm
	s.Capabilities = caps
	return s, nil
}
