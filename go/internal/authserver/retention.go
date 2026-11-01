package authserver

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention of the auth service's own history tables (plan P3). Nothing reads auth_service.user_sessions
// (access tokens are stateless JWTs; the row is only a record of an issued token) and a blacklist entry
// only matters until the token it revokes has expired, so expired rows are pure clutter that otherwise
// grows forever (1,176 of 1,223 session rows were already expired when this was written).

// RetentionDB runs a statement and reports the rows affected.
type RetentionDB interface {
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
}

// PoolRetentionDB adapts a pgxpool.Pool to RetentionDB.
type PoolRetentionDB struct{ Pool *pgxpool.Pool }

// Exec implements RetentionDB.
func (p PoolRetentionDB) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := p.Pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RetentionConfig controls the purge.
type RetentionConfig struct {
	// Enabled switches the whole job off when false. Env THE_M_AUTH_RETENTION_ENABLED (default true).
	Enabled bool
	// SessionGrace is how long after expiry a session row is kept. Env THE_M_AUTH_RETENTION_SESSIONS_DAYS
	// (default 7; 0 keeps sessions forever). Blacklist rows are removed as soon as they expire.
	SessionGrace time.Duration
	// Interval between purges. Env THE_M_AUTH_RETENTION_INTERVAL_MINUTES (default 60).
	Interval time.Duration
	// Batch is the maximum rows deleted per statement (keeps locks short). Default 2000.
	Batch int
}

// RetentionConfigFromEnv reads the configuration from the environment.
func RetentionConfigFromEnv() RetentionConfig {
	return RetentionConfig{
		Enabled:      envBool("THE_M_AUTH_RETENTION_ENABLED", true),
		SessionGrace: time.Duration(envInt("THE_M_AUTH_RETENTION_SESSIONS_DAYS", 7)) * 24 * time.Hour,
		Interval:     time.Duration(envInt("THE_M_AUTH_RETENTION_INTERVAL_MINUTES", 60)) * time.Minute,
		Batch:        2000,
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

// maxBatchesPerRun bounds one purge so a huge backlog is worked off over several runs, not in one go.
const maxBatchesPerRun = 50

const purgeSessionsSQL = `
DELETE FROM auth_service.user_sessions
WHERE id IN (
	SELECT id FROM auth_service.user_sessions
	WHERE expires_at < CURRENT_TIMESTAMP - make_interval(secs => $1)
	ORDER BY id LIMIT $2)`

const purgeBlacklistSQL = `
DELETE FROM auth_service.blacklisted_tokens
WHERE token_hash IN (
	SELECT token_hash FROM auth_service.blacklisted_tokens
	WHERE expires_at < CURRENT_TIMESTAMP
	ORDER BY expires_at LIMIT $1)`

// PurgeExpired deletes expired sessions (older than SessionGrace past expiry) and expired blacklist rows.
// It returns how many rows of each kind were removed.
func PurgeExpired(ctx context.Context, db RetentionDB, cfg RetentionConfig) (sessions, blacklisted int64, err error) {
	batch := cfg.Batch
	if batch <= 0 {
		batch = 2000
	}
	if cfg.SessionGrace > 0 {
		for i := 0; i < maxBatchesPerRun; i++ {
			n, e := db.Exec(ctx, purgeSessionsSQL, cfg.SessionGrace.Seconds(), batch)
			if e != nil {
				return sessions, blacklisted, e
			}
			sessions += n
			if n < int64(batch) {
				break
			}
		}
	}
	for i := 0; i < maxBatchesPerRun; i++ {
		n, e := db.Exec(ctx, purgeBlacklistSQL, batch)
		if e != nil {
			return sessions, blacklisted, e
		}
		blacklisted += n
		if n < int64(batch) {
			break
		}
	}
	return sessions, blacklisted, nil
}

// RunRetention purges once at startup and then every cfg.Interval until ctx is cancelled.
// Safe to run on several replicas: the deletes are idempotent.
func RunRetention(ctx context.Context, db RetentionDB, cfg RetentionConfig, log *slog.Logger) {
	if !cfg.Enabled {
		log.Info("auth retention disabled")
		return
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	log.Info("auth retention started", "interval", cfg.Interval, "session_grace", cfg.SessionGrace)
	purge := func() {
		s, b, err := PurgeExpired(ctx, db, cfg)
		if err != nil {
			log.Warn("auth retention: purge failed", "error", err)
			return
		}
		if s > 0 || b > 0 {
			log.Info("auth retention: purged expired rows", "sessions", s, "blacklisted_tokens", b)
		}
	}
	purge()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}
