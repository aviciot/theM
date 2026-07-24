package dal

import (
	"context"
)

// ListApplications returns all applications ordered by creation date.
func (d *DB) ListApplications(ctx context.Context) ([]Application, error) {
	const q = `SELECT id::text, name, enabled FROM them.applications ORDER BY created_at`

	rows, err := d.q.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	apps := make([]Application, 0)
	for rows.Next() {
		var a Application
		if err := rows.Scan(&a.ID, &a.Name, &a.Enabled); err != nil {
			return nil, err
		}
		apps = append(apps, a)
	}
	return apps, nil
}

// GetApplication returns a single application by UUID id.
func (d *DB) GetApplication(ctx context.Context, id string) (Application, error) {
	const q = `SELECT id::text, name, enabled FROM them.applications WHERE id=$1::uuid`

	var a Application
	row := d.q.QueryRow(ctx, q, id)
	if err := row.Scan(&a.ID, &a.Name, &a.Enabled); err != nil {
		return a, err
	}
	return a, nil
}

// CreateApplication inserts a new application row and returns the new UUID.
func (d *DB) CreateApplication(ctx context.Context, name string, enabled bool) (string, error) {
	const q = `INSERT INTO them.applications (name, enabled) VALUES ($1, $2) RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, name, enabled)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// UpdateApplication modifies an existing application row identified by UUID id.
func (d *DB) UpdateApplication(ctx context.Context, id, name string, enabled bool) error {
	const q = `UPDATE them.applications SET name=$2, enabled=$3, updated_at=now() WHERE id=$1::uuid`
	return d.q.Exec(ctx, q, id, name, enabled)
}

// DeleteApplication soft-deletes an application by setting enabled=false.
func (d *DB) DeleteApplication(ctx context.Context, id string) error {
	const q = `UPDATE them.applications SET enabled=false, updated_at=now() WHERE id=$1::uuid`
	return d.q.Exec(ctx, q, id)
}

// ListEntryPoints returns all entry points for a given application UUID.
// Returns an empty (non-nil) slice on DB error so callers can safely range over it.
func (d *DB) ListEntryPoints(ctx context.Context, appID string) []EntryPoint {
	const q = `
		SELECT id::text, application_id::text, slug, entry_point_type, enabled
		FROM them.entry_points WHERE application_id=$1::uuid ORDER BY created_at`

	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return make([]EntryPoint, 0)
	}
	defer rows.Close()

	eps := make([]EntryPoint, 0)
	for rows.Next() {
		var ep EntryPoint
		if err := rows.Scan(&ep.ID, &ep.ApplicationID, &ep.Slug, &ep.EntryPointType, &ep.Enabled); err != nil {
			break
		}
		eps = append(eps, ep)
	}
	return eps
}

// CreateEntryPoint inserts a new entry point row and returns the new UUID.
func (d *DB) CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled bool) (string, error) {
	const q = `
		INSERT INTO them.entry_points (application_id, slug, entry_point_type, enabled)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text`

	var id string
	row := d.q.ExecReturning(ctx, q, appID, slug, epType, enabled)
	if err := row.Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GetEntryPointSlug returns the slug of an entry point by its UUID and parent appID.
// Used for cache invalidation before rename.
func (d *DB) GetEntryPointSlug(ctx context.Context, epID, appID string) (string, error) {
	row := d.q.QueryRow(ctx,
		`SELECT slug FROM them.entry_points WHERE id=$1::uuid AND application_id=$2::uuid`, epID, appID)
	var slug string
	if err := row.Scan(&slug); err != nil {
		return "", err
	}
	return slug, nil
}

// UpdateEntryPoint modifies an existing entry point row.
func (d *DB) UpdateEntryPoint(ctx context.Context, epID, appID, slug, epType string, enabled bool) error {
	const q = `
		UPDATE them.entry_points
		SET slug=$3, entry_point_type=$4, enabled=$5, updated_at=now()
		WHERE id=$1::uuid AND application_id=$2::uuid`
	return d.q.Exec(ctx, q, epID, appID, slug, epType, enabled)
}

// DeleteEntryPoint soft-deletes an entry point by setting enabled=false.
func (d *DB) DeleteEntryPoint(ctx context.Context, epID, appID string) error {
	const q = `UPDATE them.entry_points SET enabled=false, updated_at=now() WHERE id=$1::uuid AND application_id=$2::uuid`
	return d.q.Exec(ctx, q, epID, appID)
}

// ListEPSlugsForApp returns all EP slugs for a given application UUID.
// Used by the cache invalidation helper when an application is modified/deleted.
func (d *DB) ListEPSlugsForApp(ctx context.Context, appID string) []string {
	const q = `SELECT slug FROM them.entry_points WHERE application_id = $1::uuid`
	rows, err := d.q.Query(ctx, q, appID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			break
		}
		slugs = append(slugs, slug)
	}
	return slugs
}
