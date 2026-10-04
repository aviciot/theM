package dal

import "context"

// AppFlowHTTPParam is one stored credential/param for an HTTP node.
type AppFlowHTTPParam struct {
	NodeID            string  `json:"node_id"`
	ParamKey          string  `json:"param_key"`
	ValueEncrypted    *string `json:"value_encrypted,omitempty"` // nil = not yet set
	InjectMode        string  `json:"inject_mode"`
	InjectHeaderName  *string `json:"inject_header_name,omitempty"`
}

// ListAppFlowHTTPParams returns all stored HTTP params for one application.
func (d *DB) ListAppFlowHTTPParams(ctx context.Context, applicationID string) ([]AppFlowHTTPParam, error) {
	const q = `
SELECT node_id, param_key, value_encrypted, inject_mode, inject_header_name
  FROM them.app_flow_http_params
 WHERE application_id = $1::uuid
 ORDER BY node_id, param_key`

	rows, err := d.q.Query(ctx, q, applicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppFlowHTTPParam
	for rows.Next() {
		var p AppFlowHTTPParam
		if err := rows.Scan(&p.NodeID, &p.ParamKey, &p.ValueEncrypted, &p.InjectMode, &p.InjectHeaderName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// UpsertAppFlowHTTPParam stores or updates a credential/param for one HTTP node.
func (d *DB) UpsertAppFlowHTTPParam(ctx context.Context, applicationID, nodeID, paramKey, valueEncrypted, injectMode, injectHeaderName string) error {
	var headerName *string
	if injectHeaderName != "" {
		headerName = &injectHeaderName
	}
	const q = `
INSERT INTO them.app_flow_http_params (application_id, node_id, param_key, value_encrypted, inject_mode, inject_header_name, updated_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6, now())
ON CONFLICT (application_id, node_id, param_key)
DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted,
              inject_mode = EXCLUDED.inject_mode,
              inject_header_name = EXCLUDED.inject_header_name,
              updated_at = now()`
	return d.q.Exec(ctx, q, applicationID, nodeID, paramKey, valueEncrypted, injectMode, headerName)
}

// GetAppFlowHTTPNodeCredential returns the decrypted credential value for one
// HTTP node's param key. Returns ("", nil) when no row exists (not yet set).
// Value stored is plaintext for this iteration (Fernet encryption deferred —
// same deferral as app_params plaintext scalars in the current implementation).
func (d *DB) GetAppFlowHTTPNodeCredential(ctx context.Context, applicationID, nodeID, paramKey string) (string, error) {
	const q = `
SELECT COALESCE(value_encrypted, ''), inject_mode, COALESCE(inject_header_name, '')
  FROM them.app_flow_http_params
 WHERE application_id = $1::uuid AND node_id = $2 AND param_key = $3`
	var val, mode, headerName string
	err := d.q.QueryRow(ctx, q, applicationID, nodeID, paramKey).Scan(&val, &mode, &headerName)
	if IsNoRows(err) {
		return "", nil
	}
	return val, err
}
