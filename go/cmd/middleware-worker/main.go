// Command middleware-worker is the them-middleware-worker binary.
// It polls them.middleware_jobs for pending artifact scan jobs, runs the
// enabled processor pipeline, and publishes live progress events via Redis.
//
// Configuration (env vars):
//
//	DATABASE_HOST / DATABASE_PORT / DATABASE_NAME / DATABASE_USER / DATABASE_PASSWORD
//	REDIS_HOST / REDIS_PORT / REDIS_PASSWORD / REDIS_DB
//	CLAMAV_SOCKET              — Unix socket path (default /var/run/clamav/clamd.sock)
//	MIDDLEWARE_WORKER_CONCURRENCY — goroutine pool size (default 8)
//	MIDDLEWARE_POLL_INTERVAL_MS  — poll interval when queue is empty (default 500)
//	LOG_LEVEL / LOG_FORMAT
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/middleware/av"
	"github.com/aviciot/them/internal/storage"
	"github.com/aviciot/them/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	tel := telemetry.New(cfg.LogLevel, cfg.LogFormat, cfg.InstanceID)
	log := tel.Logger
	log.Info("middleware-worker: starting", "config", cfg.SafeString())

	// ── Connect to Postgres ───────────────────────────────────────────────────
	ctx := context.Background()
	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer database.Close()
	log.Info("postgres connected", "host", cfg.DBHost, "db", cfg.DBName)

	// ── Connect to Redis ──────────────────────────────────────────────────────
	redisAddr := fmt.Sprintf("%s:%d", cfg.RedisHost, cfg.RedisPort)
	redisCache, err := cache.New(ctx, redisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer redisCache.Close()
	log.Info("redis connected", "addr", redisAddr)

	// ── Build storage client ──────────────────────────────────────────────────
	var objStore middleware.ObjectStore
	if cfg.S3Endpoint != "" {
		sc, scErr := storage.New(storage.Config{
			Endpoint:         cfg.S3Endpoint,
			AccessKey:        cfg.S3AccessKey,
			SecretKey:        cfg.S3SecretKey,
			QuarantineBucket: cfg.S3QuarantineBucket,
			ArtifactsBucket:  cfg.S3ArtifactsBucket,
		})
		if scErr != nil {
			log.Warn("storage client init failed — quarantine path unavailable", "err", scErr)
		} else {
			objStore = sc
			log.Info("storage client initialised", "endpoint", cfg.S3Endpoint)
		}
	} else {
		log.Warn("THE_M_S3_ENDPOINT not set — quarantine path unavailable")
	}

	// ── Build processor registry ──────────────────────────────────────────────
	clamavSocket := envStr("CLAMAV_SOCKET", "/var/run/clamav/clamd.sock")
	reg := middleware.NewRegistry()
	reg.Register(av.New(clamavSocket))
	log.Info("processors registered", "processors", reg.Names(), "clamav_socket", clamavSocket)

	// ── Worker pool ───────────────────────────────────────────────────────────
	concurrency := envInt("MIDDLEWARE_WORKER_CONCURRENCY", 8)
	pollInterval := time.Duration(envInt("MIDDLEWARE_POLL_INTERVAL_MS", 500)) * time.Millisecond

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Graceful shutdown on SIGTERM / SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Info("middleware-worker: shutdown signal received", "signal", sig)
		cancel()
	}()

	// ── Quarantine reaper ─────────────────────────────────────────────────────
	reaperInterval := time.Duration(envInt("REAPER_INTERVAL_MINUTES", 15)) * time.Minute
	reaper := middleware.NewReaper(&pgxQuerier{pool: database.Pool()}, objStore, log)
	go reaper.Run(ctx, reaperInterval)
	log.Info("quarantine reaper started", "interval", reaperInterval)

	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerLoop(ctx, log.With("worker_id", workerID), database.Pool(), redisCache, reg, objStore, pollInterval)
		}(i)
	}

	log.Info("middleware-worker: pool running", "concurrency", concurrency, "poll_interval_ms", pollInterval.Milliseconds())
	wg.Wait()
	log.Info("middleware-worker: all workers stopped")
	return nil
}

// workerLoop runs the claim→process→commit cycle for one goroutine.
func workerLoop(
	ctx context.Context,
	log *slog.Logger,
	pool *pgxpool.Pool,
	redisCache *cache.Cache,
	reg *middleware.Registry,
	store middleware.ObjectStore,
	pollInterval time.Duration,
) {
	dal := middleware.NewJobDAL(&pgxQuerier{pool: pool})
	pipeline := middleware.NewPipeline(reg)
	redisPub := &rueidisPublisher{client: redisCache}

	for {
		if ctx.Err() != nil {
			return
		}

		job, err := dal.Claim(ctx)
		if err != nil {
			log.Error("claim error", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
			continue
		}

		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
			continue
		}

		processJob(ctx, log, dal, pipeline, redisPub, store, job)
	}
}

// processJob executes one job: load bytes → run pipeline → commit result.
func processJob(
	ctx context.Context,
	log *slog.Logger,
	dal *middleware.JobDAL,
	pipeline *middleware.Pipeline,
	redisPub *rueidisPublisher,
	store middleware.ObjectStore,
	job *middleware.Job,
) {
	log.Info("job claimed", "job_id", job.ID, "artifact_id", job.ArtifactID, "processors", job.Processors)

	if err := dal.LoadFileBytes(ctx, job, store); err != nil {
		log.Error("load file bytes failed", "job_id", job.ID, "err", err)
		_ = dal.Fail(ctx, job, time.Now().Add(30*time.Second))
		return
	}

	// Load application security config (for processor-level settings)
	secCfg, err := dal.LoadSecurityConfig(ctx, job.ApplicationID)
	if err != nil {
		log.Error("load security config failed", "job_id", job.ID, "err", err)
		_ = dal.Fail(ctx, job, time.Now().Add(30*time.Second))
		return
	}

	// Build part from loaded bytes
	part := middleware.Part{
		Kind:     "file",
		Bytes:    job.FileBytes,
		FileName: job.FileName,
		MimeType: job.MimeType,
	}

	// Progress publisher for this job
	pub := middleware.NewScanPublisher(redisPub, job.ArtifactID, job.RunID)

	// Run pipeline
	pipelineResult := pipeline.Run(ctx, part, job.Processors, secCfg, pub)

	result := middleware.JobResult{
		FinalStatus: pipelineResult.FinalStatus,
		Threat:      pipelineResult.Threat,
		Results:     pipelineResult.Results,
		TotalMS:     pipelineResult.TotalMS,
		ScannedAt:   time.Now().UTC(),
	}

	if err := dal.Complete(ctx, job, result, store); err != nil {
		log.Error("commit result failed", "job_id", job.ID, "err", err)
		_ = dal.Fail(ctx, job, time.Now().Add(30*time.Second))
		return
	}

	// Write audit log (non-fatal)
	if err := dal.WriteAudit(ctx, job, result); err != nil {
		log.Warn("audit write failed", "job_id", job.ID, "err", err)
	}

	// Publish final result to Redis run channel
	pub.PublishFinalResult(ctx, result, job.FileName, job.FileSize)

	// Notify the Services page that stats may have changed.
	// Uses the dashboard WS channel so the frontend can re-fetch without polling.
	_ = redisPub.Publish(ctx, "them:dash:services:stats", []byte(`{}`))

	log.Info("job complete",
		"job_id", job.ID,
		"artifact_id", job.ArtifactID,
		"status", result.FinalStatus,
		"threat", result.Threat,
		"total_ms", result.TotalMS,
	)
}

// ── pgxQuerier adapts pgxpool.Pool to middleware.Querier ─────────────────────

type pgxQuerier struct {
	pool *pgxpool.Pool
}

func (q *pgxQuerier) Query(ctx context.Context, sql string, args ...any) (middleware.RowScanner, error) {
	rows, err := q.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}

func (q *pgxQuerier) QueryRow(ctx context.Context, sql string, args ...any) middleware.SingleRowScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

func (q *pgxQuerier) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.pool.Exec(ctx, sql, args...)
	return err
}

type pgxRows struct {
	rows interface {
		Next() bool
		Scan(dest ...any) error
		Close()
		Err() error
	}
}

func (r *pgxRows) Next() bool         { return r.rows.Next() }
func (r *pgxRows) Scan(dst ...any) error { return r.rows.Scan(dst...) }
func (r *pgxRows) Close() error        { r.rows.Close(); return r.rows.Err() }

// ── rueidisPublisher adapts cache.Cache to middleware.RedisPublisher ──────────

type rueidisPublisher struct {
	client *cache.Cache
}

func (p *rueidisPublisher) Publish(ctx context.Context, channel string, payload []byte) error {
	rc := p.client.Client()
	return rc.Do(ctx, rc.B().Publish().Channel(channel).Message(string(payload)).Build()).Error()
}

// ── env helpers ───────────────────────────────────────────────────────────────

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
