package dal

import "context"

// llmProviderKeySelectCols is the column list shared by all llm_provider_keys queries.
const llmProviderKeySelectCols = `
	SELECT id, llm_provider_id, tenant_id, name, api_key_encrypted,
	       is_default, last_tested_at::text, last_test_ok
	FROM them.llm_provider_keys`

// scanProviderKey scans one llm_provider_keys row from r into an LLMProviderKey value.
func scanProviderKey(r RowScanner) (LLMProviderKey, error) {
	var k LLMProviderKey
	if err := r.Scan(
		&k.ID, &k.LLMProviderID, &k.TenantID, &k.Name, &k.APIKeyEncrypted,
		&k.IsDefault, &k.LastTestedAt, &k.LastTestOK,
	); err != nil {
		return k, err
	}
	return k, nil
}

// ListLLMProviderKeys returns all named keys for (llmProviderID, tenantID), ordered by name ASC.
func (d *DB) ListLLMProviderKeys(ctx context.Context, llmProviderID int64, tenantID string) ([]LLMProviderKey, error) {
	rows, err := d.q.Query(ctx,
		llmProviderKeySelectCols+" WHERE llm_provider_id=$1 AND tenant_id=$2::uuid ORDER BY name ASC",
		llmProviderID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]LLMProviderKey, 0)
	for rows.Next() {
		k, err := scanProviderKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// GetLLMProviderKey returns a single named key by id, scoped to tenantID.
// Returns pgx.ErrNoRows when not found or not owned by tenantID.
func (d *DB) GetLLMProviderKey(ctx context.Context, id int64, tenantID string) (LLMProviderKey, error) {
	row := d.q.QueryRow(ctx,
		llmProviderKeySelectCols+" WHERE id=$1 AND tenant_id=$2::uuid", id, tenantID)
	return scanProviderKey(&singleToRow{s: row})
}

// GetDefaultLLMProviderKey returns the key marked is_default for (llmProviderID, tenantID).
// Returns pgx.ErrNoRows when no default key is set.
func (d *DB) GetDefaultLLMProviderKey(ctx context.Context, llmProviderID int64, tenantID string) (LLMProviderKey, error) {
	row := d.q.QueryRow(ctx,
		llmProviderKeySelectCols+" WHERE llm_provider_id=$1 AND tenant_id=$2::uuid AND is_default=true",
		llmProviderID, tenantID)
	return scanProviderKey(&singleToRow{s: row})
}

// CreateLLMProviderKey inserts a new named key. Returns a unique-violation error
// (SQLSTATE 23505) when (llm_provider_id, tenant_id, name) already exists.
func (d *DB) CreateLLMProviderKey(ctx context.Context, in LLMProviderKeyInput) (LLMProviderKey, error) {
	const q = `
		INSERT INTO them.llm_provider_keys
		  (llm_provider_id, tenant_id, name, api_key_encrypted, is_default)
		VALUES ($1, $2::uuid, $3, $4, $5)
		RETURNING id, llm_provider_id, tenant_id, name, api_key_encrypted,
		          is_default, last_tested_at::text, last_test_ok`

	row := d.q.ExecReturning(ctx, q,
		in.LLMProviderID, in.TenantID, in.Name, in.APIKeyEncrypted, in.IsDefault,
	)
	return scanProviderKey(&singleToRow{s: row})
}

// UpdateLLMProviderKey applies a full replacement UPDATE to the key row identified by
// (id, tenantID). The caller is responsible for merging patch fields (fetch-then-modify).
// Returns pgx.ErrNoRows when the key does not exist or is not owned by tenantID.
func (d *DB) UpdateLLMProviderKey(ctx context.Context, id int64, tenantID string, in LLMProviderKeyInput) (LLMProviderKey, error) {
	const q = `
		UPDATE them.llm_provider_keys
		SET name=$3, api_key_encrypted=$4, is_default=$5, updated_at=now()
		WHERE id=$1 AND tenant_id=$2::uuid
		RETURNING id, llm_provider_id, tenant_id, name, api_key_encrypted,
		          is_default, last_tested_at::text, last_test_ok`

	row := d.q.ExecReturning(ctx, q, id, tenantID, in.Name, in.APIKeyEncrypted, in.IsDefault)
	return scanProviderKey(&singleToRow{s: row})
}

// SetLLMProviderKeyTestResult records the outcome of a Test-key probe call.
func (d *DB) SetLLMProviderKeyTestResult(ctx context.Context, id int64, tenantID string, ok bool) error {
	const q = `
		UPDATE them.llm_provider_keys
		SET last_tested_at=now(), last_test_ok=$3
		WHERE id=$1 AND tenant_id=$2::uuid`
	return d.q.Exec(ctx, q, id, tenantID, ok)
}

// ClearDefaultLLMProviderKeys unsets is_default on every key for (llmProviderID, tenantID).
// Used inside SetDefaultLLMProviderKey's swap so the partial unique index
// (llm_provider_keys_one_default_uq) is never violated.
func (d *DB) ClearDefaultLLMProviderKeys(ctx context.Context, llmProviderID int64, tenantID string) error {
	const q = `
		UPDATE them.llm_provider_keys
		SET is_default=false, updated_at=now()
		WHERE llm_provider_id=$1 AND tenant_id=$2::uuid AND is_default=true`
	return d.q.Exec(ctx, q, llmProviderID, tenantID)
}

// SetDefaultLLMProviderKey marks the given key as the default for its (provider, tenant),
// clearing any previously-default key first. Returns pgx.ErrNoRows when the key does
// not exist or is not owned by tenantID.
//
// Not run inside an explicit transaction: DB is a thin Querier wrapper with no common
// BeginTx surface across the admin-pool and tenant-tx adapters it wraps. The clear-then-set
// sequence is safe here because both statements are scoped to the same single caller-owned
// (llm_provider_id, tenant_id) and the partial unique index rejects any inconsistent end state.
func (d *DB) SetDefaultLLMProviderKey(ctx context.Context, id int64, tenantID string) (LLMProviderKey, error) {
	key, err := d.GetLLMProviderKey(ctx, id, tenantID)
	if err != nil {
		return LLMProviderKey{}, err
	}
	if err := d.ClearDefaultLLMProviderKeys(ctx, key.LLMProviderID, tenantID); err != nil {
		return LLMProviderKey{}, err
	}
	const q = `
		UPDATE them.llm_provider_keys
		SET is_default=true, updated_at=now()
		WHERE id=$1 AND tenant_id=$2::uuid
		RETURNING id, llm_provider_id, tenant_id, name, api_key_encrypted,
		          is_default, last_tested_at::text, last_test_ok`
	row := d.q.ExecReturning(ctx, q, id, tenantID)
	return scanProviderKey(&singleToRow{s: row})
}

// DeleteLLMProviderKey hard-deletes a named key scoped to tenantID.
// Returns pgx.ErrNoRows when the key does not exist or is not owned by tenantID.
func (d *DB) DeleteLLMProviderKey(ctx context.Context, id int64, tenantID string) error {
	const q = `DELETE FROM them.llm_provider_keys WHERE id=$1 AND tenant_id=$2::uuid RETURNING id`
	row := d.q.ExecReturning(ctx, q, id, tenantID)
	var deleted int64
	return row.Scan(&deleted)
}

// ── LLM provider key types ────────────────────────────────────────────────────

// LLMProviderKey is the internal DB row representation of them.llm_provider_keys.
// APIKeyEncrypted holds the raw stored value (with "enc:" prefix). Masking and
// decryption happen exclusively in the service layer.
type LLMProviderKey struct {
	ID              int64
	LLMProviderID   int64
	TenantID        string
	Name            string
	APIKeyEncrypted string
	IsDefault       bool
	LastTestedAt    *string
	LastTestOK      *bool
}

// LLMProviderKeyInput is used for both CREATE and full-UPDATE (fetch-then-modify).
// APIKeyEncrypted must be pre-encrypted by the service layer.
type LLMProviderKeyInput struct {
	LLMProviderID   int64
	TenantID        string
	Name            string
	APIKeyEncrypted string
	IsDefault       bool
}
