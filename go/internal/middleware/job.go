package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ObjectStore is the minimal interface job.go needs from the storage layer.
type ObjectStore interface {
	GetQuarantine(ctx context.Context, key string) ([]byte, error)
	DeleteQuarantine(ctx context.Context, key string) error
	PutArtifact(ctx context.Context, key string, data []byte, contentType string) error
}

// Job represents one row from them.middleware_jobs as claimed by a worker.
type Job struct {
	ID            string
	ArtifactID    string // quarantine_artifacts.id (= the future run_artifacts.id)
	QuarantineID  string // same as ArtifactID for quarantine-path jobs
	ApplicationID string
	RunID         string // may be empty
	SessionID     string // may be empty
	Processors    []string
	AttemptCount  int
	MaxAttempts   int

	// Loaded separately by LoadFileBytes
	FileName    string
	MimeType    string
	FileSize    int64
	FileBytes   []byte
	StorageKey  string // MinIO quarantine key
}

// JobResult is written back to the DB after the pipeline completes.
type JobResult struct {
	FinalStatus string
	Threat      string
	Results     []Result
	TotalMS     int64
	ScannedAt   time.Time
}

// Querier is the minimal DB interface needed by the job DAL.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (RowScanner, error)
	QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner
	Exec(ctx context.Context, sql string, args ...any) error
}

// RowScanner iterates over query rows.
type RowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
}

// SingleRowScanner scans one row.
type SingleRowScanner interface {
	Scan(dest ...any) error
}

// JobDAL handles database operations for the middleware job queue.
type JobDAL struct {
	q Querier
}

// NewJobDAL creates a JobDAL.
func NewJobDAL(q Querier) *JobDAL {
	return &JobDAL{q: q}
}

// EnqueueWithQuarantine inserts a pending job linked to a quarantine_artifacts row.
// artifactID and quarantineID are the same UUID (quarantine_artifacts.id).
func (d *JobDAL) EnqueueWithQuarantine(
	ctx context.Context,
	artifactID, quarantineID, applicationID, runID, sessionID string,
	processors []string,
) error {
	const q = `
INSERT INTO them.middleware_jobs
  (artifact_id, quarantine_id, application_id, run_id, session_id, processors)
VALUES
  ($1::uuid, $2::uuid, $3::uuid,
   CASE WHEN $4 = '' THEN NULL ELSE $4::uuid END,
   CASE WHEN $5 = '' THEN NULL ELSE $5::uuid END,
   $6)`
	return d.q.Exec(ctx, q, artifactID, quarantineID, applicationID, runID, sessionID, processors)
}

// Enqueue inserts a new pending job (legacy path — no quarantine row).
// Kept for compatibility; new code should use EnqueueWithQuarantine.
func (d *JobDAL) Enqueue(ctx context.Context, artifactID, applicationID, runID, sessionID string, processors []string) error {
	const q = `
INSERT INTO them.middleware_jobs
  (artifact_id, application_id, run_id, session_id, processors)
VALUES
  ($1::uuid, $2::uuid,
   CASE WHEN $3 = '' THEN NULL ELSE $3::uuid END,
   CASE WHEN $4 = '' THEN NULL ELSE $4::uuid END,
   $5)`
	return d.q.Exec(ctx, q, artifactID, applicationID, runID, sessionID, processors)
}

// Claim atomically claims the next unclaimed pending job using SKIP LOCKED.
// Returns nil if no job is available.
func (d *JobDAL) Claim(ctx context.Context) (*Job, error) {
	const q = `
UPDATE them.middleware_jobs
SET    status = 'claimed', claimed_at = now(), updated_at = now()
WHERE  id = (
  SELECT j.id
  FROM   them.middleware_jobs j
  WHERE  j.status = 'pending'
    AND  j.attempt_count < j.max_attempts
    AND  (j.retry_after IS NULL OR j.retry_after <= now())
  ORDER  BY j.created_at
  LIMIT  1
  FOR UPDATE SKIP LOCKED
)
RETURNING
  id::text, artifact_id::text, COALESCE(quarantine_id::text,''),
  application_id::text,
  COALESCE(run_id::text,''), COALESCE(session_id::text,''),
  processors, attempt_count, max_attempts`

	var job Job
	var procs []string
	row := d.q.QueryRow(ctx, q)
	if err := row.Scan(
		&job.ID, &job.ArtifactID, &job.QuarantineID,
		&job.ApplicationID,
		&job.RunID, &job.SessionID,
		&procs, &job.AttemptCount, &job.MaxAttempts,
	); err != nil {
		return nil, nil // no rows = no work
	}
	job.Processors = procs
	return &job, nil
}

// LoadFileBytes reads file metadata from quarantine_artifacts and fetches
// the bytes from MinIO quarantine bucket via store.
// Falls back to reading run_artifacts.data when quarantine_id is empty
// (legacy jobs created before migration 051).
func (d *JobDAL) LoadFileBytes(ctx context.Context, job *Job, store ObjectStore) error {
	if job.QuarantineID != "" {
		return d.loadFromQuarantine(ctx, job, store)
	}
	return d.loadFromArtifacts(ctx, job)
}

func (d *JobDAL) loadFromQuarantine(ctx context.Context, job *Job, store ObjectStore) error {
	const q = `
SELECT COALESCE(filename,''), COALESCE(content_type,''), COALESCE(size,0), COALESCE(storage_key,'')
FROM   them.quarantine_artifacts
WHERE  id = $1::uuid`
	if err := d.q.QueryRow(ctx, q, job.QuarantineID).Scan(
		&job.FileName, &job.MimeType, &job.FileSize, &job.StorageKey,
	); err != nil {
		return fmt.Errorf("load quarantine metadata: %w", err)
	}
	if job.StorageKey == "" {
		return fmt.Errorf("quarantine %s: storage_key is empty (bytes already scrubbed?)", job.QuarantineID)
	}
	data, err := store.GetQuarantine(ctx, job.StorageKey)
	if err != nil {
		return fmt.Errorf("load quarantine bytes: %w", err)
	}
	job.FileBytes = data
	return nil
}

func (d *JobDAL) loadFromArtifacts(ctx context.Context, job *Job) error {
	const q = `
SELECT COALESCE(data, ''), COALESCE(filename,''), COALESCE(content_type,''), COALESCE(size,0)
FROM   them.run_artifacts
WHERE  id = $1::uuid`
	return d.q.QueryRow(ctx, q, job.ArtifactID).Scan(
		&job.FileBytes, &job.FileName, &job.MimeType, &job.FileSize,
	)
}

// LoadSecurityConfig loads the security_config JSONB for an application.
func (d *JobDAL) LoadSecurityConfig(ctx context.Context, applicationID string) (SecurityConfig, error) {
	const q = `SELECT COALESCE(security_config,'{}') FROM them.applications WHERE id = $1::uuid`
	var raw []byte
	if err := d.q.QueryRow(ctx, q, applicationID).Scan(&raw); err != nil {
		return DefaultSecurityConfig(), nil
	}
	var cfg SecurityConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultSecurityConfig(), nil
	}
	return MergeDefaults(cfg), nil
}

// Complete marks the job done and writes the scan result back.
//
// Quarantine-path (job.QuarantineID != ""):
//   - clean/error: promote bytes from quarantine → artifacts bucket;
//     INSERT run_artifacts row with the same UUID; delete quarantine bytes.
//   - infected:    INSERT run_artifacts metadata only (data=NULL, storage_key=NULL);
//     delete quarantine bytes immediately.
//
// Legacy path (job.QuarantineID == ""):
//   - update run_artifacts scan columns in-place (old behaviour).
func (d *JobDAL) Complete(ctx context.Context, job *Job, res JobResult, store ObjectStore) error {
	if job.QuarantineID != "" {
		return d.completeQuarantinePath(ctx, job, res, store)
	}
	return d.completeLegacyPath(ctx, job, res)
}

func (d *JobDAL) completeQuarantinePath(
	ctx context.Context,
	job *Job,
	res JobResult,
	store ObjectStore,
) error {
	resultJSON, _ := json.Marshal(res.Results)

	artifactsKey := "artifacts/" + job.ArtifactID

	switch res.FinalStatus {
	case "clean", "error":
		// Promote bytes to artifacts bucket.
		if len(job.FileBytes) > 0 {
			if err := store.PutArtifact(ctx, artifactsKey, job.FileBytes, job.MimeType); err != nil {
				return fmt.Errorf("promote to artifacts: %w", err)
			}
		}
		// Insert clean run_artifacts row (no BYTEA — bytes in MinIO).
		const insertClean = `
INSERT INTO them.run_artifacts
  (id, run_id, application_id, session_id, filename, content_type, size,
   storage_key, scan_status, scan_result, scanned_at, tenant_id)
VALUES
  ($1::uuid, $2::uuid, $3::uuid,
   CASE WHEN $4 = '' THEN NULL ELSE $4::uuid END,
   $5, $6, $7, $8, $9, $10::jsonb, $11,
   (SELECT tenant_id FROM them.applications WHERE id = $3::uuid LIMIT 1))
ON CONFLICT (id) DO UPDATE
  SET scan_status = EXCLUDED.scan_status,
      scan_result = EXCLUDED.scan_result,
      scanned_at  = EXCLUDED.scanned_at,
      storage_key = EXCLUDED.storage_key`
		if err := d.q.Exec(ctx, insertClean,
			job.ArtifactID, job.RunID, job.ApplicationID, job.SessionID,
			job.FileName, job.MimeType, job.FileSize,
			artifactsKey, res.FinalStatus, resultJSON, res.ScannedAt,
		); err != nil {
			return fmt.Errorf("insert clean artifact: %w", err)
		}

	case "infected":
		// Insert metadata-only row — no bytes, no storage_key.
		const insertInfected = `
INSERT INTO them.run_artifacts
  (id, run_id, application_id, session_id, filename, content_type, size,
   data, storage_key, scan_status, scan_result, scanned_at, tenant_id)
VALUES
  ($1::uuid, $2::uuid, $3::uuid,
   CASE WHEN $4 = '' THEN NULL ELSE $4::uuid END,
   $5, $6, $7,
   NULL, NULL, 'infected', $8::jsonb, $9,
   (SELECT tenant_id FROM them.applications WHERE id = $3::uuid LIMIT 1))
ON CONFLICT (id) DO UPDATE
  SET scan_status = 'infected',
      scan_result = EXCLUDED.scan_result,
      scanned_at  = EXCLUDED.scanned_at,
      data        = NULL,
      storage_key = NULL`
		if err := d.q.Exec(ctx, insertInfected,
			job.ArtifactID, job.RunID, job.ApplicationID, job.SessionID,
			job.FileName, job.MimeType, job.FileSize,
			resultJSON, res.ScannedAt,
		); err != nil {
			return fmt.Errorf("insert infected artifact: %w", err)
		}

	default:
		return fmt.Errorf("complete: unexpected final status %q", res.FinalStatus)
	}

	// Delete quarantine bytes from MinIO (fire and forget errors — bytes will
	// expire via MinIO lifecycle policy anyway).
	if job.StorageKey != "" {
		_ = store.DeleteQuarantine(ctx, job.StorageKey)
	}
	// Null out storage_key in quarantine row to signal bytes are gone.
	_ = d.q.Exec(ctx, `UPDATE them.quarantine_artifacts SET storage_key = NULL WHERE id = $1::uuid`, job.QuarantineID)

	// Update job row.
	return d.markJobDone(ctx, job, res)
}

func (d *JobDAL) completeLegacyPath(ctx context.Context, job *Job, res JobResult) error {
	resultJSON, _ := json.Marshal(res.Results)

	const updateArtifact = `
UPDATE them.run_artifacts
SET scan_status = $2,
    scan_result = $3::jsonb,
    scanned_at  = $4
WHERE id = $1::uuid`
	if err := d.q.Exec(ctx, updateArtifact,
		job.ArtifactID, res.FinalStatus, resultJSON, res.ScannedAt,
	); err != nil {
		return err
	}
	return d.markJobDone(ctx, job, res)
}

func (d *JobDAL) markJobDone(ctx context.Context, job *Job, res JobResult) error {
	jobResultJSON, _ := json.Marshal(map[string]any{
		"final_status": res.FinalStatus,
		"threat":       res.Threat,
		"total_ms":     res.TotalMS,
		"processors":   res.Results,
	})
	const updateJob = `
UPDATE them.middleware_jobs
SET status = 'done', result = $2::jsonb, updated_at = now()
WHERE id = $1::uuid`
	return d.q.Exec(ctx, updateJob, job.ID, jobResultJSON)
}

// Fail increments attempt_count and schedules a retry (or marks permanently failed).
func (d *JobDAL) Fail(ctx context.Context, job *Job, retryAfter time.Time) error {
	const q = `
UPDATE them.middleware_jobs
SET attempt_count = attempt_count + 1,
    status        = CASE WHEN attempt_count + 1 >= max_attempts THEN 'failed' ELSE 'pending' END,
    retry_after   = CASE WHEN attempt_count + 1 >= max_attempts THEN NULL ELSE $2 END,
    claimed_at    = NULL,
    updated_at    = now()
WHERE id = $1::uuid`
	return d.q.Exec(ctx, q, job.ID, retryAfter)
}

// WriteAudit inserts one row per processor outcome into them.middleware_audit.
func (d *JobDAL) WriteAudit(ctx context.Context, job *Job, res JobResult) error {
	names := job.Processors
	for i, r := range res.Results {
		procName := ""
		if i < len(names) {
			procName = names[i]
		}
		detailJSON, _ := json.Marshal(r.Detail)
		const q = `
INSERT INTO them.middleware_audit
  (artifact_id, application_id, session_id, run_id, processor, outcome, detail, duration_ms)
VALUES
  ($1::uuid, $2::uuid,
   CASE WHEN $3 = '' THEN NULL ELSE $3::uuid END,
   CASE WHEN $4 = '' THEN NULL ELSE $4::uuid END,
   $5, $6, $7::jsonb, $8)`
		if err := d.q.Exec(ctx, q,
			job.ArtifactID, job.ApplicationID, job.SessionID, job.RunID,
			procName, r.Outcome, detailJSON, r.DurationMS,
		); err != nil {
			return err
		}
	}
	return nil
}
