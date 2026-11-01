// Package retention deletes old operational data on a schedule (plan P3). Nothing else in the platform
// expires runs, tokens, gateway history or audit logs, so they grow forever.
//
// Each Policy names a table, a filter and a retention period in days. A period of 0 disables the policy,
// so only clearly-expired technical data (temporary tokens) is on by default; run data, chat history,
// gateway history and audit logs stay until an operator sets a period. Deletes run in small batches under
// a cross-pod advisory lock on one dedicated connection. THE_M_RETENTION_DRY_RUN (default true) only
// counts and logs what would be deleted.
//
// Deleting a run also removes its steps, usage, artifacts and approvals (cascades, db/127) and queues its
// stored files for the middleware-worker janitor. Chat history (tasks/task_messages) survives run deletion
// on purpose, so it has its own policy.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/aviciot/them/internal/domain"
)

// lockKey is the advisory-lock key (must not collide with the reconciler's 987654321).
const lockKey = int64(987654322)

// maxBatchesPerPolicy bounds one run so a large backlog is worked off over several runs.
const maxBatchesPerPolicy = 50

// Policy is one table's retention rule.
type Policy struct {
	Name  string // short id; also the env var infix THE_M_RETENTION_<NAME>_DAYS
	Table string // schema-qualified table
	Key   string // unique column used to batch deletes
	Where string // rows eligible for deletion; $1 is the retention period in seconds (float8)
	Days  int    // 0 = disabled
}

func (p Policy) deleteSQL() string {
	return fmt.Sprintf(`DELETE FROM %[1]s WHERE %[2]s IN (SELECT %[2]s FROM %[1]s WHERE %[3]s ORDER BY %[2]s LIMIT $2)`, p.Table, p.Key, p.Where)
}

func (p Policy) countSQL() string {
	return fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, p.Table, p.Where)
}

// DefaultPolicies returns the built-in policies with the given retention days by policy name.
func DefaultPolicies(days map[string]int) []Policy {
	return []Policy{
		{
			// Temporary tokens made by the Admin Playground and the test-runner. Real EP tokens are never touched.
			Name: "ACCESS_TOKENS", Table: "them.access_tokens", Key: "id", Days: days["ACCESS_TOKENS"],
			Where: `expires_at IS NOT NULL AND expires_at < now() - make_interval(secs => $1) AND label ~ '^(playground-|tr-)'`,
		},
		{
			// Finished runs. Cascades to steps, usage, artifacts, approvals; stored files go to the janitor queue.
			Name: "RUNS", Table: "them.runs", Key: "id", Days: days["RUNS"],
			Where: `status IN (` + domain.TerminalRunStatusesSQL + `) AND COALESCE(ended_at, started_at) < now() - make_interval(secs => $1)`,
		},
		{
			// Conversation history (tasks + task_messages + task artifacts). Removes the oldest turns of a conversation.
			Name: "CHAT", Table: "them.tasks", Key: "id", Days: days["CHAT"],
			Where: `state IN ('completed','failed','canceled','rejected') AND updated_at < now() - make_interval(secs => $1)`,
		},
		{
			Name: "GATEWAY", Table: "them.gateway_requests", Key: "id", Days: days["GATEWAY"],
			Where: `created_at < now() - make_interval(secs => $1)`,
		},
		{
			Name: "AUDIT", Table: "them.audit_logs", Key: "id", Days: days["AUDIT"],
			Where: `created_at < now() - make_interval(secs => $1)`,
		},
	}
}

// Config controls the job.
type Config struct {
	Policies []Policy
	Interval time.Duration // between runs
	Batch    int           // rows per DELETE
	DryRun   bool          // count and log only
}

// ConfigFromEnv reads the configuration from the environment.
//
//	THE_M_RETENTION_DRY_RUN            default true
//	THE_M_RETENTION_INTERVAL_MINUTES   default 60
//	THE_M_RETENTION_<NAME>_DAYS        ACCESS_TOKENS (default 7), RUNS, CHAT, GATEWAY, AUDIT (default 0 = keep forever)
func ConfigFromEnv() Config {
	days := map[string]int{"ACCESS_TOKENS": 7}
	for _, n := range []string{"ACCESS_TOKENS", "RUNS", "CHAT", "GATEWAY", "AUDIT"} {
		days[n] = envInt("THE_M_RETENTION_"+n+"_DAYS", days[n])
	}
	return Config{
		Policies: DefaultPolicies(days),
		Interval: time.Duration(envInt("THE_M_RETENTION_INTERVAL_MINUTES", 60)) * time.Minute,
		Batch:    1000,
		DryRun:   envBool("THE_M_RETENTION_DRY_RUN", true),
	}
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

var (
	metricDeleted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "retention", Name: "deleted_total",
		Help: "Rows deleted by retention, by policy.",
	}, []string{"policy"})
	metricErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "retention", Name: "errors_total",
		Help: "Retention errors (lock, count or delete failures).",
	})
)

// DB is what the job needs from Postgres.
type DB interface {
	// AcquireLock takes a session-level advisory lock on one dedicated connection (ok=false: held elsewhere).
	AcquireLock(ctx context.Context, key int64) (release func(), ok bool, err error)
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Count(ctx context.Context, sql string, args ...any) (int64, error)
}

// PgxDB adapts a pgxpool.Pool (use the admin pool: the job spans tenants) to DB.
type PgxDB struct{ pool *pgxpool.Pool }

// NewPgxDB wraps a pool.
func NewPgxDB(p *pgxpool.Pool) *PgxDB { return &PgxDB{pool: p} }

// AcquireLock implements DB. The lock is taken and released on the same pooled connection; if the
// unlock fails the connection is closed so PostgreSQL drops the lock instead of returning it to the pool.
func (d *PgxDB) AcquireLock(ctx context.Context, key int64) (func(), bool, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !locked {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Hijack().Close(ctx)
			return
		}
		conn.Release()
	}, true, nil
}

// Exec implements DB.
func (d *PgxDB) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := d.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Count implements DB.
func (d *PgxDB) Count(ctx context.Context, sql string, args ...any) (int64, error) {
	var n int64
	err := d.pool.QueryRow(ctx, sql, args...).Scan(&n)
	return n, err
}

// RunOnce applies every enabled policy once, under the cross-pod lock. It returns rows deleted (or, in
// dry-run, rows that would be deleted) per policy name.
func RunOnce(ctx context.Context, db DB, cfg Config, log *slog.Logger) map[string]int64 {
	out := map[string]int64{}
	release, ok, err := db.AcquireLock(ctx, lockKey)
	if err != nil {
		log.Warn("retention: advisory lock failed", "error", err)
		metricErrors.Inc()
		return out
	}
	if !ok {
		log.Debug("retention: another pod holds the lock — skipping")
		return out
	}
	defer release()

	batch := cfg.Batch
	if batch <= 0 {
		batch = 1000
	}
	for _, p := range cfg.Policies {
		if p.Days <= 0 {
			continue
		}
		secs := float64(p.Days) * 24 * 3600
		if cfg.DryRun {
			n, err := db.Count(ctx, p.countSQL(), secs)
			if err != nil {
				log.Warn("retention: count failed", "policy", p.Name, "error", err)
				metricErrors.Inc()
				continue
			}
			out[p.Name] = n
			if n > 0 {
				log.Info("retention: [dry-run] would delete rows", "policy", p.Name, "table", p.Table, "days", p.Days, "rows", n)
			}
			continue
		}
		var total int64
		for i := 0; i < maxBatchesPerPolicy; i++ {
			n, err := db.Exec(ctx, p.deleteSQL(), secs, batch)
			if err != nil {
				log.Warn("retention: delete failed", "policy", p.Name, "error", err)
				metricErrors.Inc()
				break
			}
			total += n
			if n < int64(batch) {
				break
			}
		}
		if total > 0 {
			metricDeleted.WithLabelValues(p.Name).Add(float64(total))
			log.Info("retention: deleted rows", "policy", p.Name, "table", p.Table, "days", p.Days, "rows", total)
		}
		out[p.Name] = total
	}
	return out
}

// Run applies the policies at startup and then every cfg.Interval until ctx is cancelled.
func Run(ctx context.Context, db DB, cfg Config, log *slog.Logger) {
	enabled := 0
	for _, p := range cfg.Policies {
		if p.Days > 0 {
			enabled++
		}
	}
	log.Info("retention started", "interval", cfg.Interval, "dry_run", cfg.DryRun, "enabled_policies", enabled)
	if enabled == 0 {
		return
	}
	RunOnce(ctx, db, cfg, log)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunOnce(ctx, db, cfg, log)
		}
	}
}
