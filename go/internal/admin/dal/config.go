package dal

import "context"

// ConfigRow is a row from them.config.
// Value holds raw JSONB bytes; callers unmarshal into their own types.
type ConfigRow struct {
	Key   string
	Value []byte
}

// GetConfig returns the config row for key, or (nil, nil) if not found.
func (d *DB) GetConfig(ctx context.Context, key string) (*ConfigRow, error) {
	const q = `SELECT config_key, config_value::text FROM them.config WHERE config_key=$1`
	var row ConfigRow
	err := d.q.QueryRow(ctx, q, key).Scan(&row.Key, &row.Value)
	if IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// UpsertConfig inserts or updates a config row.
func (d *DB) UpsertConfig(ctx context.Context, key string, value []byte) error {
	const q = `
		INSERT INTO them.config (config_key, config_value, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (config_key) DO UPDATE
		  SET config_value = EXCLUDED.config_value,
		      updated_at   = now()`
	return d.q.Exec(ctx, q, key, string(value))
}
