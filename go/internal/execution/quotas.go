package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxUserQuotaChecker queries per-user daily spend from run_usage via pgxpool.
type pgxUserQuotaChecker struct {
	pool *pgxpool.Pool
}

// NewUserQuotaChecker creates a UserQuotaChecker that queries the database.
func NewUserQuotaChecker(pool *pgxpool.Pool) UserQuotaChecker {
	return &pgxUserQuotaChecker{pool: pool}
}

// GetUserDailySpend returns the total USD spent by the user today.
// Queries run_usage table for all runs WHERE user_id = userID AND created_at >= today.
func (qc *pgxUserQuotaChecker) GetUserDailySpend(ctx context.Context, userID int64) (float64, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	const q = `
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM them.run_usage
		WHERE user_id = $1 AND created_at >= $2
	`
	var total float64
	err := qc.pool.QueryRow(ctx, q, userID, today).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("daily spend query failed: %w", err)
	}
	return total, nil
}
