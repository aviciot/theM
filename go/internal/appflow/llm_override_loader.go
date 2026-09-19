package appflow

import (
	"context"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// PgxAppFlowLLMOverrideLoader reads stored inline-LLM-node overrides from PostgreSQL.
// Used by Lifecycle.StartAppFlow to apply Runtime-screen overrides at workflow start time.
type PgxAppFlowLLMOverrideLoader struct {
	pools *db.Pools
}

// NewPgxAppFlowLLMOverrideLoader creates a loader backed by the given db.Pools.
func NewPgxAppFlowLLMOverrideLoader(pools *db.Pools) *PgxAppFlowLLMOverrideLoader {
	return &PgxAppFlowLLMOverrideLoader{pools: pools}
}

// Load returns node_id -> LLMOverride for the given applicationID.
// Returns nil, error on DB failure — caller skips overrides (fail-open).
func (l *PgxAppFlowLLMOverrideLoader) Load(ctx context.Context, applicationID string) (map[string]LLMOverride, error) {
	d := dal.NewDBFromAdminQuerier(l.pools.NewAdminQuerier())

	rows, err := d.ListAppFlowLLMOverrides(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string]LLMOverride, len(rows))
	for _, row := range rows {
		overrides[row.NodeID] = LLMOverride{Provider: row.Provider, Model: row.Model}
	}
	return overrides, nil
}
