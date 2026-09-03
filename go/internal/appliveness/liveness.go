// Package appliveness probes all enabled entry points every 30s and
// publishes reachability results to Redis so the dashboard UI can show
// live/unreachable status on application cards without polling.
//
// Flow:
//
//	Loop() → listEnabledEPSlugs() → probeAll() → publish()
//	                                                  ↓
//	                             Redis SET  them:dash:app_status_cache  (snapshot for new WS clients)
//	                             Redis PUB  them:dash:apps              (live push to connected clients)
package appliveness

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/db"
)

const (
	probeInterval = 30 * time.Second
	probeTimeout  = 5 * time.Second
	cacheKey      = "them:dash:app_status_cache"
	cacheTTL      = 120 * time.Second
	pubChannel    = "them:dash:apps"
)

// Liveness is the reachability result for one entry point slug.
type Liveness struct {
	Reachable bool   `json:"reachable"`
	LatencyMs *int64 `json:"latency_ms"`
}

// publisher is the Redis surface needed by this package.
// Satisfied by rueidisPublisher in production and fakePublisher in tests.
type publisher interface {
	setCache(ctx context.Context, key, val string) error
	publish(ctx context.Context, channel, msg string) error
}

// rueidisPublisher wraps a real rueidis.Client.
type rueidisPublisher struct{ rc rueidis.Client }

func (r *rueidisPublisher) setCache(ctx context.Context, key, val string) error {
	return r.rc.Do(ctx, r.rc.B().Set().Key(key).Value(val).Ex(cacheTTL).Build()).Error()
}

func (r *rueidisPublisher) publish(ctx context.Context, channel, msg string) error {
	return r.rc.Do(ctx, r.rc.B().Publish().Channel(channel).Message(msg).Build()).Error()
}

// Loop probes all enabled entry points immediately on startup, then every 30s.
// Stops when ctx is cancelled. Run as a background goroutine.
//
// pools is optional: when non-nil its Admin pool (BYPASSRLS) is used for the
// cross-tenant entry_points query so that RLS on entry_points does not filter
// out non-bootstrap tenants. legacyPool is the fallback used when pools is nil.
func Loop(ctx context.Context, legacyPool *pgxpool.Pool, pools *db.Pools, rc rueidis.Client, selfPort int, log *slog.Logger) {
	pub := &rueidisPublisher{rc: rc}
	run(ctx, legacyPool, pools, pub, selfPort, log)

	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run(ctx, legacyPool, pools, pub, selfPort, log)
		}
	}
}

// run is one full probe-and-publish cycle. Separated from Loop for testability.
func run(ctx context.Context, legacyPool *pgxpool.Pool, pools *db.Pools, pub publisher, selfPort int, log *slog.Logger) {
	queryPool := legacyPool
	if pools != nil {
		queryPool = pools.Admin
	}
	slugs, err := listEnabledEPSlugs(ctx, queryPool)
	if err != nil {
		log.Warn("appliveness: db query failed", "error", err)
		return
	}
	if len(slugs) == 0 {
		return
	}

	results := probeAll(slugs, selfPort)

	if err := publishResults(ctx, pub, results); err != nil {
		log.Warn("appliveness: publish failed", "error", err)
		return
	}

	log.Debug("appliveness: cycle complete", "probed", len(results))
}

// probeAll concurrently GETs /apps/{slug} on localhost and returns results.
func probeAll(slugs []string, selfPort int) map[string]Liveness {
	results := make(map[string]Liveness, len(slugs))
	var mu sync.Mutex
	var wg sync.WaitGroup

	client := &http.Client{Timeout: probeTimeout}
	base := fmt.Sprintf("http://localhost:%d", selfPort)

	for _, slug := range slugs {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			t0 := time.Now()
			resp, err := client.Get(base + "/apps/" + s)
			ms := time.Since(t0).Milliseconds()

			ok := err == nil && resp.StatusCode < 500
			if resp != nil {
				resp.Body.Close()
			}

			var latPtr *int64
			if ok {
				latPtr = &ms
			}

			mu.Lock()
			results[s] = Liveness{Reachable: ok, LatencyMs: latPtr}
			mu.Unlock()
		}(slug)
	}
	wg.Wait()
	return results
}

// publishResults writes to the Redis cache (for snapshot-on-subscribe) and
// pub/sub channel (for live push to already-connected dashboard WS clients).
func publishResults(ctx context.Context, pub publisher, results map[string]Liveness) error {
	statusJSON, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := pub.setCache(ctx, cacheKey, string(statusJSON)); err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	eventJSON, _ := json.Marshal(map[string]any{
		"type":     "app_status",
		"statuses": json.RawMessage(statusJSON),
	})
	if err := pub.publish(ctx, pubChannel, string(eventJSON)); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	return nil
}

// listEnabledEPSlugs returns slugs for all entry points where both the entry
// point and its parent application are enabled.
func listEnabledEPSlugs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	const q = `
		SELECT ep.slug
		FROM them.entry_points ep
		JOIN them.applications a ON a.id = ep.application_id
		WHERE ep.enabled = true AND a.enabled = true`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}
