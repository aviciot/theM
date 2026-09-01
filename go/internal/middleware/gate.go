package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// FileGate intercepts file artifacts in the A2A run-stream, stores them in
// them.run_artifacts with scan_status='pending', and enqueues a middleware job.
// Callers get back a gated artifact ID they can use to build a download URL.
//
// Security design:
//   - Files are fetched via HTTP from the internal download_url (agent-supplied)
//   - Files exceeding MaxFileMB are rejected before storage
//   - File bytes are stored in the existing run_artifacts.data column
//   - scan_status starts at 'pending'; worker updates it after scanning
type FileGate struct {
	db         GateQuerier
	httpClient *http.Client

	cacheMu sync.Mutex
	cache   map[string]cachedSecCfg // appID → config+expiry
}

type cachedSecCfg struct {
	cfg    SecurityConfig
	expiry time.Time
}

// GateQuerier is the minimal DB interface needed by FileGate.
// It is a subset of Querier (no multi-row Query needed).
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

// GateResult is returned by Intercept.
type GateResult struct {
	// ArtifactID is the UUID of the stored run_artifact row.
	ArtifactID string
	// ScanStatus is the initial status ('pending' or 'disabled').
	ScanStatus string
	// Enqueued is true if a middleware job was created.
	Enqueued bool
}

// NewFileGate creates a FileGate.
func NewFileGate(db GateQuerier) *FileGate {
	return &FileGate{
		db:         db,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cache:      make(map[string]cachedSecCfg),
	}
}

// Intercept processes one file artifact from the A2A run-stream.
// If security scanning is not enabled for the application, it returns
// (GateResult{ScanStatus:"disabled"}, nil) and the caller should proceed
// with the original download_url unchanged.
//
// If scanning is enabled it:
//  1. Fetches the file bytes from in.DownloadURL
//  2. Stores them in them.run_artifacts (with scan_status='pending')
//  3. Enqueues a middleware_job
//
// The caller should use the returned ArtifactID to construct a gated
// download URL: /api/v1/runs/{run_id}/artifacts/{artifact_id}
func (g *FileGate) Intercept(ctx context.Context, in GateInput) (GateResult, error) {
	cfg, err := g.loadSecCfg(ctx, in.ApplicationID)
	if err != nil {
		return GateResult{ScanStatus: "disabled"}, nil // fail open
	}
	if !cfg.Enabled {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	// Determine enabled processors for file parts (canonical order, file-applicable only)
	processors := enabledFileProcessors(cfg)
	if len(processors) == 0 {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	// Fetch file bytes from the agent-supplied URL
	maxBytes := int64(5 * 1024 * 1024) // default 5MB
	if avCfg, ok := cfg.Processors["av_scan"]; ok {
		var av AVScanConfig
		if json.Unmarshal(avCfg, &av) == nil && av.MaxFileMB > 0 {
			maxBytes = int64(av.MaxFileMB) * 1024 * 1024
		}
	}

	data, err := g.fetchFile(ctx, in.DownloadURL, maxBytes)
	if err != nil {
		// Fail open: if we can't fetch the file, don't block the artifact
		return GateResult{ScanStatus: "disabled"}, nil
	}

	// Store artifact in run_artifacts with scan_status='pending'
	artifactID, err := g.storeArtifact(ctx, in, data)
	if err != nil {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	// Enqueue middleware job
	dal := NewJobDAL(g.db)
	if err := dal.Enqueue(ctx, artifactID, in.ApplicationID, in.RunID, in.SessionID, processors); err != nil {
		// Non-fatal: artifact is stored; worker may still pick it up
		return GateResult{ArtifactID: artifactID, ScanStatus: "pending", Enqueued: false}, nil
	}

	return GateResult{ArtifactID: artifactID, ScanStatus: "pending", Enqueued: true}, nil
}

// loadSecCfg returns the security config for an application with a 30s in-memory cache.
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
// Called when the admin updates the security config via Redis pub/sub.
func (g *FileGate) InvalidateCache(appID string) {
	g.cacheMu.Lock()
	delete(g.cache, appID)
	g.cacheMu.Unlock()
}

// fetchFile GETs the URL and returns up to maxBytes. Returns an error if
// the response exceeds maxBytes or the request fails.
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

// enabledFileProcessors returns the ordered list of processor names applicable
// to file parts that are enabled in cfg. Does not require a Registry.
func enabledFileProcessors(cfg SecurityConfig) []string {
	if !cfg.Enabled {
		return nil
	}
	// Canonical file-applicable processors in pipeline order
	fileProcessors := []string{"av_scan", "audit_capture"}
	var out []string
	for _, name := range fileProcessors {
		raw, ok := cfg.Processors[name]
		if !ok {
			continue
		}
		// Check processor-level enabled flag
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

// InterceptInline processes an inline (already-decoded) file artifact.
// It is equivalent to Intercept but skips the HTTP fetch step, using the
// caller-supplied bytes directly. Use this when the file data is already in
// memory (e.g. base64-decoded artifacts from the orchestrator path).
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

	maxBytes := int64(5 * 1024 * 1024)
	if avCfg, ok := cfg.Processors["av_scan"]; ok {
		var av AVScanConfig
		if json.Unmarshal(avCfg, &av) == nil && av.MaxFileMB > 0 {
			maxBytes = int64(av.MaxFileMB) * 1024 * 1024
		}
	}
	if int64(len(data)) > maxBytes {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	artifactID, err := g.storeArtifact(ctx, in, data)
	if err != nil {
		return GateResult{ScanStatus: "disabled"}, nil
	}

	dal := NewJobDAL(g.db)
	if err := dal.Enqueue(ctx, artifactID, in.ApplicationID, in.RunID, in.SessionID, processors); err != nil {
		return GateResult{ArtifactID: artifactID, ScanStatus: "pending", Enqueued: false}, nil
	}

	return GateResult{ArtifactID: artifactID, ScanStatus: "pending", Enqueued: true}, nil
}

// storeArtifact inserts one row into them.run_artifacts with scan_status='pending'.
// Returns the new artifact UUID.
func (g *FileGate) storeArtifact(ctx context.Context, in GateInput, data []byte) (string, error) {
	ct := in.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	fn := in.FileName
	if fn == "" {
		fn = "artifact"
	}

	const q = `
INSERT INTO them.run_artifacts
  (run_id, application_id, session_id, filename, content_type, size, data, scan_status, tenant_id)
VALUES
  ($1::uuid, $2::uuid,
   CASE WHEN $3 = '' THEN NULL ELSE $3::uuid END,
   $4, $5, $6, $7, 'pending',
   (SELECT tenant_id FROM them.applications WHERE id = $2::uuid LIMIT 1))
RETURNING id::text`

	var id string
	err := g.db.QueryRow(ctx, q,
		in.RunID, in.ApplicationID, in.SessionID,
		fn, ct, int64(len(data)), data,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("gate: store artifact: %w", err)
	}
	return id, nil
}
