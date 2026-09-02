package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store is the minimal object-storage interface needed by FileGate.
// Implemented by *storage.Client in production; a fake in tests.
type Store interface {
	PutQuarantine(ctx context.Context, key string, data []byte, contentType string) error
}

// FileGate intercepts file artifacts in the A2A run-stream. When security
// scanning is enabled for an application it:
//   1. Writes bytes to the MinIO quarantine bucket (NOT Postgres)
//   2. Inserts a quarantine_artifacts metadata row in Postgres
//   3. Enqueues a middleware_job referencing the quarantine row
//
// Files never touch run_artifacts until the worker confirms them clean.
type FileGate struct {
	db         GateQuerier
	store      Store
	httpClient *http.Client

	cacheMu sync.Mutex
	cache   map[string]cachedSecCfg // appID → config+expiry
}

type cachedSecCfg struct {
	cfg    SecurityConfig
	expiry time.Time
}

// GateQuerier is the minimal DB interface needed by FileGate.
type GateQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) error
	QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner
	Query(ctx context.Context, sql string, args ...any) (RowScanner, error)
}

// GateInput carries the file event fields needed to intercept a file.
type GateInput struct {
	// From the run-stream "file" event
	DownloadURL string
	FileName    string
	ContentType string

	// From the execution context
	ApplicationID string
	RunID         string
	SessionID     string
	TenantID      string
}

// GateResult is returned by Intercept / InterceptInline.
type GateResult struct {
	// ArtifactID is the UUID of the quarantine_artifacts row.
	// (Becomes a run_artifacts row only after a clean scan.)
	ArtifactID string
	// ScanStatus is the initial status ('pending' or 'disabled').
	ScanStatus string
	// Enqueued is true if a middleware job was created.
	Enqueued bool
}

// NewFileGate creates a FileGate. store may be nil only when security is always
// disabled (e.g. in tests that only exercise the disabled path).
func NewFileGate(db GateQuerier, store Store) *FileGate {
	return &FileGate{
		db:         db,
		store:      store,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cache:      make(map[string]cachedSecCfg),
	}
}

// Intercept processes one file artifact from the A2A run-stream.
// If security scanning is not enabled for the application it returns
// (GateResult{ScanStatus:"disabled"}, nil) and the caller should proceed
// with the original download_url unchanged.
//
// If scanning is enabled it:
//  1. Fetches the file bytes from in.DownloadURL
//  2. Writes bytes to the MinIO quarantine bucket
//  3. Inserts a quarantine_artifacts metadata row
//  4. Enqueues a middleware_job
//
// The returned ArtifactID is the quarantine_artifacts UUID; it becomes a
// run_artifacts UUID only after a clean scan (same UUID is reused).
func (g *FileGate) Intercept(ctx context.Context, in GateInput) (GateResult, error) {
	cfg, err := g.loadSecCfg(ctx, in.ApplicationID)
	if err != nil {
		return GateResult{ScanStatus: "disabled"}, nil
	}
	if !cfg.Enabled {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	processors := enabledFileProcessors(cfg)
	if len(processors) == 0 {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	maxBytes := maxFileBytesFromCfg(cfg)

	data, err := g.fetchFile(ctx, in.DownloadURL, maxBytes)
	if err != nil {
		return GateResult{ScanStatus: "disabled"}, nil // fail open
	}

	return g.quarantineAndEnqueue(ctx, in, cfg, data, processors)
}

// InterceptInline processes an already-decoded file artifact (bytes in memory).
// Equivalent to Intercept but skips the HTTP fetch step.
func (g *FileGate) InterceptInline(ctx context.Context, in GateInput, data []byte) (GateResult, error) {
	cfg, err := g.loadSecCfg(ctx, in.ApplicationID)
	if err != nil {
		return GateResult{ScanStatus: "disabled"}, nil
	}
	if !cfg.Enabled {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	processors := enabledFileProcessors(cfg)
	if len(processors) == 0 {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	maxBytes := maxFileBytesFromCfg(cfg)
	if int64(len(data)) > maxBytes {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	return g.quarantineAndEnqueue(ctx, in, cfg, data, processors)
}

// quarantineAndEnqueue is the shared path for Intercept and InterceptInline
// once bytes are in hand and we know security is enabled.
func (g *FileGate) quarantineAndEnqueue(
	ctx context.Context,
	in GateInput,
	cfg SecurityConfig,
	data []byte,
	processors []string,
) (GateResult, error) {
	// Object storage is required for quarantine. If it wasn't configured at
	// startup (no S3 endpoint), fail open so the run isn't killed.
	if g.store == nil {
		return GateResult{ScanStatus: "disabled"}, nil
	}
	_ = cfg // reserved for future per-processor options

	ct := in.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	fn := in.FileName
	if fn == "" {
		fn = "artifact"
	}

	// Generate a stable UUID for this quarantine entry.
	qID := uuid.New().String()
	storageKey := "quarantine/" + qID

	// 1. Write bytes to MinIO quarantine bucket.
	if err := g.store.PutQuarantine(ctx, storageKey, data, ct); err != nil {
		// Fail open: can't quarantine → don't block delivery
		return GateResult{ScanStatus: "disabled"}, nil
	}

	// 2. Insert quarantine_artifacts metadata row (no file bytes in Postgres).
	if err := g.storeQuarantine(ctx, qID, storageKey, in, fn, ct, data); err != nil {
		// Try to clean up quarantine object; ignore error
		_ = g.cleanupQuarantine(ctx, storageKey)
		return GateResult{ScanStatus: "disabled"}, nil
	}

	// 3. Enqueue middleware job with quarantine_id.
	dal := NewJobDAL(g.db)
	if err := dal.EnqueueWithQuarantine(ctx, qID, qID, in.ApplicationID, in.RunID, in.SessionID, processors); err != nil {
		return GateResult{ArtifactID: qID, ScanStatus: "pending", Enqueued: false}, nil
	}

	return GateResult{ArtifactID: qID, ScanStatus: "pending", Enqueued: true}, nil
}

// storeQuarantine inserts a row into them.quarantine_artifacts (no BYTEA stored).
func (g *FileGate) storeQuarantine(
	ctx context.Context,
	qID, storageKey string,
	in GateInput,
	fn, ct string,
	data []byte,
) error {
	const q = `
INSERT INTO them.quarantine_artifacts
  (id, application_id, run_id, session_id, tenant_id, filename, content_type, size, storage_key)
VALUES
  ($1::uuid, $2::uuid, $3::uuid,
   CASE WHEN $4 = '' THEN NULL ELSE $4::uuid END,
   (SELECT tenant_id FROM them.applications WHERE id = $2::uuid LIMIT 1),
   $5, $6, $7, $8)`

	return g.db.Exec(ctx, q,
		qID, in.ApplicationID, in.RunID, in.SessionID,
		fn, ct, int64(len(data)), storageKey,
	)
}

// cleanupQuarantine is a best-effort MinIO delete used on rollback paths.
// It uses a short context so it doesn't block the caller.
type storeDeleter interface {
	Store
	DeleteQuarantine(ctx context.Context, key string) error
}

func (g *FileGate) cleanupQuarantine(ctx context.Context, key string) error {
	if d, ok := g.store.(storeDeleter); ok {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return d.DeleteQuarantine(cctx, key)
	}
	return nil
}

// loadSecCfg returns the security config for an application with a 30s cache.
func (g *FileGate) loadSecCfg(ctx context.Context, appID string) (SecurityConfig, error) {
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()

	if cached, ok := g.cache[appID]; ok && time.Now().Before(cached.expiry) {
		return cached.cfg, nil
	}

	const q = `SELECT COALESCE(security_config, '{}') FROM them.applications WHERE id = $1::uuid`
	var raw []byte
	if err := g.db.QueryRow(ctx, q, appID).Scan(&raw); err != nil {
		return DefaultSecurityConfig(), err
	}
	var cfg SecurityConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultSecurityConfig(), nil
	}
	cfg = MergeDefaults(cfg)
	g.cache[appID] = cachedSecCfg{cfg: cfg, expiry: time.Now().Add(30 * time.Second)}
	return cfg, nil
}

// InvalidateCache evicts the security config cache entry for appID.
func (g *FileGate) InvalidateCache(appID string) {
	g.cacheMu.Lock()
	delete(g.cache, appID)
	g.cacheMu.Unlock()
}

// fetchFile GETs the URL and returns up to maxBytes.
func (g *FileGate) fetchFile(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gate: build request: %w", err)
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gate: fetch file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gate: upstream %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gate: read body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("gate: file exceeds %d bytes", maxBytes)
	}
	return data, nil
}

// maxFileBytesFromCfg reads the av_scan.max_file_mb setting; defaults to 5 MB.
func maxFileBytesFromCfg(cfg SecurityConfig) int64 {
	const defaultMB = 5
	if avCfg, ok := cfg.Processors["av_scan"]; ok {
		var av AVScanConfig
		if json.Unmarshal(avCfg, &av) == nil && av.MaxFileMB > 0 {
			return int64(av.MaxFileMB) * 1024 * 1024
		}
	}
	return defaultMB * 1024 * 1024
}

// enabledFileProcessors returns the ordered list of processor names applicable
// to file parts that are enabled in cfg.
func enabledFileProcessors(cfg SecurityConfig) []string {
	if !cfg.Enabled {
		return nil
	}
	fileProcessors := []string{"av_scan", "audit_capture"}
	var out []string
	for _, name := range fileProcessors {
		raw, ok := cfg.Processors[name]
		if !ok {
			continue
		}
		var base struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(raw, &base) == nil && !base.Enabled {
			continue
		}
		out = append(out, name)
	}
	return out
}
