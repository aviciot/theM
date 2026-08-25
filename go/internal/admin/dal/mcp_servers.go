package dal

import (
	"context"
	"encoding/json"
	"time"
)

// MCPServer is the DB row representation of them.mcp_servers.
type MCPServer struct {
	ID                  string
	TenantID            string
	Name                string
	Slug                string
	Description         string
	Transport           string
	URL                 string
	AuthType            string
	HealthStatus        string
	LastCheckedAt       *time.Time
	LastError           string
	ToolsManifest       json.RawMessage
	Capabilities        json.RawMessage
	Enabled             bool
	CreatedAt           string
	UpdatedAt           string
	ProbeCredentialSet  bool // true when probe_credential_encrypted is non-empty
}

// MCPServerInput is used for CREATE and UPDATE.
type MCPServerInput struct {
	TenantID                string
	Name                    string
	Slug                    string
	Description             string
	Transport               string
	URL                     string
	AuthType                string
	Enabled                 bool
	ProbeCredentialEncrypted *string // nil = do not change existing value; "" = clear it
}

// AppMCPCredential is a row from them.app_mcp_credentials.
type AppMCPCredential struct {
	ID                  string
	ApplicationID       string
	MCPServerID         string
	CredentialEncrypted string
	AuthHeaderName      string
}

// AppMCPCredentialMeta is the safe list view (no credential value).
type AppMCPCredentialMeta struct {
	MCPServerID    string `json:"mcp_server_id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	CredentialSet  bool   `json:"credential_set"`
	AuthHeaderName string `json:"auth_header_name"`
}

const mcpServerSelectCols = `
	SELECT id::text, tenant_id::text, name, slug,
	       COALESCE(description,''), transport, COALESCE(url,''), auth_type,
	       health_status, last_checked_at, COALESCE(last_error,''),
	       tools_manifest, capabilities, enabled,
	       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       probe_credential_encrypted IS NOT NULL AND probe_credential_encrypted <> ''
	FROM them.mcp_servers`

func scanMCPServer(r RowScanner) (MCPServer, error) {
	var s MCPServer
	var tm, caps []byte
	err := r.Scan(
		&s.ID, &s.TenantID, &s.Name, &s.Slug,
		&s.Description, &s.Transport, &s.URL, &s.AuthType,
		&s.HealthStatus, &s.LastCheckedAt, &s.LastError,
		&tm, &caps, &s.Enabled,
		&s.CreatedAt, &s.UpdatedAt,
		&s.ProbeCredentialSet,
	)
	if err != nil {
		return s, err
	}
	s.ToolsManifest = tm
	s.Capabilities = caps
	return s, nil
}

// ListMCPServers returns all servers for the given tenant ordered by created_at ASC.
func (d *DB) ListMCPServers(ctx context.Context, tenantID string) ([]MCPServer, error) {
	rows, err := d.q.Query(ctx,
		mcpServerSelectCols+" WHERE tenant_id = $1::uuid ORDER BY created_at ASC",
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MCPServer
	for rows.Next() {
		s, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if out == nil {
		out = []MCPServer{}
	}
	return out, nil
}

// GetMCPServer returns one server by ID scoped to a tenant. Returns pgx.ErrNoRows when missing.
func (d *DB) GetMCPServer(ctx context.Context, id, tenantID string) (MCPServer, error) {
	row := d.q.QueryRow(ctx,
		mcpServerSelectCols+" WHERE id = $1::uuid AND tenant_id = $2::uuid",
		id, tenantID,
	)
	return scanMCPServer(&singleToRow{s: row})
}

// CreateMCPServer inserts a new server row. Returns unique-violation when (tenant_id,slug) exists.
func (d *DB) CreateMCPServer(ctx context.Context, in MCPServerInput) (MCPServer, error) {
	const q = `
		INSERT INTO them.mcp_servers
		  (tenant_id, name, slug, description, transport, url, auth_type, enabled, probe_credential_encrypted)
		VALUES ($1::uuid, $2, $3, NULLIF($4,''), $5, NULLIF($6,''), $7, $8, NULLIF($9,''))
		RETURNING id::text, tenant_id::text, name, slug,
		          COALESCE(description,''), transport, COALESCE(url,''), auth_type,
		          health_status, last_checked_at, COALESCE(last_error,''),
		          tools_manifest, capabilities, enabled,
		          to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		          to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		          probe_credential_encrypted IS NOT NULL AND probe_credential_encrypted <> ''`
	probeEnc := ""
	if in.ProbeCredentialEncrypted != nil {
		probeEnc = *in.ProbeCredentialEncrypted
	}
	row := d.q.ExecReturning(ctx, q,
		in.TenantID, in.Name, in.Slug, in.Description,
		in.Transport, in.URL, in.AuthType, in.Enabled, probeEnc,
	)
	return scanMCPServer(&singleToRow{s: row})
}

// UpdateMCPServer updates admin-owned fields (NOT health/manifest — those are owned by mcp-service).
// When ProbeCredentialEncrypted is nil the probe credential is left unchanged;
// when it is a pointer to "" the credential is cleared.
func (d *DB) UpdateMCPServer(ctx context.Context, id, tenantID string, in MCPServerInput) (MCPServer, error) {
	const q = `
		UPDATE them.mcp_servers
		SET name=$3, slug=$4, description=NULLIF($5,''), transport=$6,
		    url=NULLIF($7,''), auth_type=$8, enabled=$9,
		    probe_credential_encrypted = CASE WHEN $10::boolean THEN NULLIF($11,'') ELSE probe_credential_encrypted END,
		    updated_at=now()
		WHERE id=$1::uuid AND tenant_id=$2::uuid
		RETURNING id::text, tenant_id::text, name, slug,
		          COALESCE(description,''), transport, COALESCE(url,''), auth_type,
		          health_status, last_checked_at, COALESCE(last_error,''),
		          tools_manifest, capabilities, enabled,
		          to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		          to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		          probe_credential_encrypted IS NOT NULL AND probe_credential_encrypted <> ''`
	updateProbe := in.ProbeCredentialEncrypted != nil
	probeEnc := ""
	if in.ProbeCredentialEncrypted != nil {
		probeEnc = *in.ProbeCredentialEncrypted
	}
	row := d.q.ExecReturning(ctx, q,
		id, tenantID, in.Name, in.Slug, in.Description,
		in.Transport, in.URL, in.AuthType, in.Enabled,
		updateProbe, probeEnc,
	)
	return scanMCPServer(&singleToRow{s: row})
}

// DeleteMCPServer hard-deletes a server. Returns pgx.ErrNoRows when not found.
func (d *DB) DeleteMCPServer(ctx context.Context, id, tenantID string) error {
	const q = `DELETE FROM them.mcp_servers WHERE id=$1::uuid AND tenant_id=$2::uuid RETURNING id`
	row := d.q.ExecReturning(ctx, q, id, tenantID)
	var deleted string
	return row.Scan(&deleted)
}

// GetAppMCPCredential returns the credential for (applicationID, serverID). Returns pgx.ErrNoRows when missing.
func (d *DB) GetAppMCPCredential(ctx context.Context, applicationID, serverID string) (AppMCPCredential, error) {
	const q = `
		SELECT id::text, application_id::text, mcp_server_id::text,
		       COALESCE(credential_encrypted,''),
		       auth_header_name
		FROM them.app_mcp_credentials
		WHERE application_id=$1::uuid AND mcp_server_id=$2::uuid`
	var c AppMCPCredential
	row := d.q.QueryRow(ctx, q, applicationID, serverID)
	err := row.Scan(&c.ID, &c.ApplicationID, &c.MCPServerID, &c.CredentialEncrypted, &c.AuthHeaderName)
	return c, err
}

// ListAppMCPCredentials returns all credentials for an application with credential_set bool only (no plaintext).
func (d *DB) ListAppMCPCredentials(ctx context.Context, applicationID string) ([]AppMCPCredentialMeta, error) {
	const q = `
		SELECT c.mcp_server_id::text, s.slug, s.name,
		       c.credential_encrypted IS NOT NULL AND c.credential_encrypted <> '',
		       c.auth_header_name
		FROM them.app_mcp_credentials c
		JOIN them.mcp_servers s ON s.id = c.mcp_server_id
		WHERE c.application_id=$1::uuid
		ORDER BY s.slug ASC`
	rows, err := d.q.Query(ctx, q, applicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppMCPCredentialMeta
	for rows.Next() {
		var m AppMCPCredentialMeta
		if err := rows.Scan(&m.MCPServerID, &m.Slug, &m.Name, &m.CredentialSet, &m.AuthHeaderName); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []AppMCPCredentialMeta{}
	}
	return out, nil
}

// UpsertAppMCPCredential inserts or replaces a credential for (applicationID, serverID).
func (d *DB) UpsertAppMCPCredential(ctx context.Context, applicationID, serverID, encryptedCred, headerName string) error {
	const q = `
		INSERT INTO them.app_mcp_credentials
		  (application_id, mcp_server_id, credential_encrypted, auth_header_name)
		VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT (application_id, mcp_server_id)
		DO UPDATE SET credential_encrypted=$3, auth_header_name=$4, updated_at=now()`
	return d.q.Exec(ctx, q, applicationID, serverID, encryptedCred, headerName)
}

// DeleteAppMCPCredential removes a credential. Returns pgx.ErrNoRows when not found.
func (d *DB) DeleteAppMCPCredential(ctx context.Context, applicationID, serverID string) error {
	const q = `
		DELETE FROM them.app_mcp_credentials
		WHERE application_id=$1::uuid AND mcp_server_id=$2::uuid
		RETURNING id`
	row := d.q.ExecReturning(ctx, q, applicationID, serverID)
	var deleted string
	return row.Scan(&deleted)
}
