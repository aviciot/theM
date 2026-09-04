package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

// quotaDal is the subset of Dal used by checkResourceQuota.
// Allows the helper to work with any Dal implementation in tests.
type quotaDal interface {
	GetQuota(ctx context.Context, tenantID string) (dal.TenantQuota, error)
}

// checkResourceQuota is shared by agent/app/mcp Create methods.
// It fetches the tenant quota, extracts the relevant limit via limitFn,
// and compares it against the current resource count from countFn.
//
// Fail-open rules:
//   - No quota row (pgx.ErrNoRows) → no enforcement.
//   - Any DB error fetching quota or counting → also fail-open (don't block legitimate creates).
//   - Nil limit pointer → no enforcement for that specific field.
func checkResourceQuota(
	ctx context.Context,
	d quotaDal,
	tenantID string,
	limitFn func(dal.TenantQuota) *int,
	countFn func() (int, error),
) error {
	q, err := d.GetQuota(ctx, tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no quota row → no enforcement
		}
		return nil // DB error → fail-open
	}
	limit := limitFn(q)
	if limit == nil {
		return nil // NULL limit → unlimited
	}
	count, err := countFn()
	if err != nil {
		return nil // count error → fail-open
	}
	if count >= *limit {
		return ErrQuotaExceeded
	}
	return nil
}
