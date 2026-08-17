package dal

import (
	"context"
	"encoding/json"
)

// AppDefinition is a row from them.application_definitions.
type AppDefinition struct {
	ID             string          `json:"id"`
	ApplicationID  string          `json:"application_id"`
	TenantID       string          `json:"tenant_id"`
	Revision       int             `json:"revision"`
	Status         string          `json:"status"` // "draft" | "published"
	Definition     json.RawMessage `json:"definition"`
	DefinitionHash string          `json:"definition_hash"`
	CreatedAt      string          `json:"created_at"`
	PublishedAt    *string         `json:"published_at,omitempty"`
}

// GetNextRevision returns COALESCE(MAX(revision),0)+1 for the given application.
func (d *DB) GetNextRevision(ctx context.Context, appID string) (int, error) {
	const q = `SELECT COALESCE(MAX(revision),0)+1 FROM them.application_definitions WHERE application_id=$1::uuid`
	var rev int
	row := d.q.QueryRow(ctx, q, appID)
	if err := row.Scan(&rev); err != nil {
		return 0, err
	}
	return rev, nil
}

// CreateDefinition inserts a new draft definition row and returns the new UUID.
// The INSERT uses a sub-SELECT to verify that the application belongs to the
// tenant — if the application is not found or belongs to a different tenant,
// the sub-SELECT returns no rows and the INSERT produces no rows, causing
// pgx.ErrNoRows on the RETURNING scan.
func (d *DB) CreateDefinition(ctx context.Context, tenantID, appID string, rev int, defJSON []byte, hash string) (string, error) {
	const q = `
		INSERT INTO them.application_definitions
			(application_id, tenant_id, revision, status, definition, definition_hash)
		SELECT $1::uuid, $2::uuid, $3, 'draft', $4::jsonb, $5
		  FROM them.applications
		 WHERE id = $1::uuid AND tenant_id = $2::uuid
		RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, appID, tenantID, rev, defJSON, hash)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GetDefinition returns a single definition row scoped to tenant + application.
// Returns pgx.ErrNoRows when not found or when it belongs to another tenant/app.
func (d *DB) GetDefinition(ctx context.Context, tenantID, appID, defID string) (AppDefinition, error) {
	const q = `
		SELECT id::text, application_id::text, tenant_id::text, revision, status,
		       definition, definition_hash,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
		       CASE WHEN published_at IS NULL THEN NULL
		            ELSE to_char(published_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		       END AS published_at
		  FROM them.application_definitions
		 WHERE id=$1::uuid AND application_id=$2::uuid AND tenant_id=$3::uuid`

	var def AppDefinition
	var publishedAt *string
	row := d.q.QueryRow(ctx, q, defID, appID, tenantID)
	if err := row.Scan(
		&def.ID,
		&def.ApplicationID,
		&def.TenantID,
		&def.Revision,
		&def.Status,
		&def.Definition,
		&def.DefinitionHash,
		&def.CreatedAt,
		&publishedAt,
	); err != nil {
		return def, err
	}
	def.PublishedAt = publishedAt
	return def, nil
}

// ListDefinitions returns all definitions for the given tenant + application,
// ordered by revision descending. Returns an empty (non-nil) slice when there
// are no rows.
func (d *DB) ListDefinitions(ctx context.Context, tenantID, appID string) ([]AppDefinition, error) {
	const q = `
		SELECT id::text, application_id::text, tenant_id::text, revision, status,
		       definition, definition_hash,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
		       CASE WHEN published_at IS NULL THEN NULL
		            ELSE to_char(published_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		       END AS published_at
		  FROM them.application_definitions
		 WHERE application_id=$1::uuid AND tenant_id=$2::uuid
		 ORDER BY revision DESC`

	rows, err := d.q.Query(ctx, q, appID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	defs := make([]AppDefinition, 0)
	for rows.Next() {
		var def AppDefinition
		var publishedAt *string
		if err := rows.Scan(
			&def.ID,
			&def.ApplicationID,
			&def.TenantID,
			&def.Revision,
			&def.Status,
			&def.Definition,
			&def.DefinitionHash,
			&def.CreatedAt,
			&publishedAt,
		); err != nil {
			return nil, err
		}
		def.PublishedAt = publishedAt
		defs = append(defs, def)
	}
	return defs, nil
}

// UpdateDraftDefinition updates definition + hash for a draft row scoped to
// tenant + application. Returns pgx.ErrNoRows if the row is not found, does
// not belong to the tenant/application, or is not a draft (status != 'draft').
func (d *DB) UpdateDraftDefinition(ctx context.Context, tenantID, appID, defID string, defJSON []byte, hash string) error {
	const q = `
		UPDATE them.application_definitions
		   SET definition=$4::jsonb, definition_hash=$5
		 WHERE id=$1::uuid AND application_id=$2::uuid AND tenant_id=$3::uuid AND status='draft'
		 RETURNING id::text`

	var id string
	return d.q.ExecReturning(ctx, q, defID, appID, tenantID, defJSON, hash).Scan(&id)
}

// DeleteDraftDefinition hard-deletes a draft definition row scoped to tenant +
// application. Returns pgx.ErrNoRows if the row is not found, does not belong
// to the tenant/application, or is not a draft (status != 'draft').
func (d *DB) DeleteDraftDefinition(ctx context.Context, tenantID, appID, defID string) error {
	const q = `
		DELETE FROM them.application_definitions
		 WHERE id=$1::uuid AND application_id=$2::uuid AND tenant_id=$3::uuid AND status='draft'
		 RETURNING id::text`

	var id string
	return d.q.ExecReturning(ctx, q, defID, appID, tenantID).Scan(&id)
}

