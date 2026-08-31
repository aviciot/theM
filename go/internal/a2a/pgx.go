package a2a

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EPCardRow holds the data needed to serve an agent card for one entry point.
type EPCardRow struct {
	// AgentCardJSON is the synthesized card (entry_points.agent_card). Nil when
	// no card has been synthesized yet.
	AgentCardJSON []byte
	// OrchestratorDisplayName is ao.display_name (fallback to ao.name).
	// Used when no synthesized card exists.
	OrchestratorDisplayName string
	// AppName is a.name — used as a last-resort fallback.
	AppName string
}

// ErrCardNotFound is returned when the (app_slug, ep_slug) pair does not exist
// or the entry point is not of type 'a2a'.
var ErrCardNotFound = errors.New("a2a: entry point not found")

// CardLoader fetches the data needed to build an agent card.
type CardLoader interface {
	LoadEPCard(ctx context.Context, appSlug, epSlug string) (EPCardRow, error)
}

// PgxCardLoader implements CardLoader against a live pgxpool.Pool.
type PgxCardLoader struct {
	pool *pgxpool.Pool
}

// NewPgxCardLoader wraps a pool as a CardLoader.
func NewPgxCardLoader(pool *pgxpool.Pool) *PgxCardLoader {
	return &PgxCardLoader{pool: pool}
}

const epCardQuery = `
SELECT
    ep.agent_card,
    COALESCE(ao.display_name, ao.name, ''),
    a.name
FROM them.entry_points ep
JOIN them.applications a  ON a.id  = ep.application_id
LEFT JOIN them.app_orchestrators ao
    ON ao.id = ep.app_orchestrator_id
   AND ao.application_id = ep.application_id
WHERE a.slug   = $1
  AND ep.slug  = $2
  AND ep.entry_point_type = 'a2a'
LIMIT 1`

// LoadEPCard fetches the card row for the given app/ep slug pair.
// Returns ErrCardNotFound when no matching a2a entry point exists.
func (l *PgxCardLoader) LoadEPCard(ctx context.Context, appSlug, epSlug string) (EPCardRow, error) {
	var row EPCardRow
	var cardJSON []byte
	err := l.pool.QueryRow(ctx, epCardQuery, appSlug, epSlug).Scan(
		&cardJSON,
		&row.OrchestratorDisplayName,
		&row.AppName,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EPCardRow{}, fmt.Errorf("%w: app=%s ep=%s", ErrCardNotFound, appSlug, epSlug)
		}
		return EPCardRow{}, fmt.Errorf("a2a: load card: %w", err)
	}
	row.AgentCardJSON = cardJSON
	return row, nil
}
