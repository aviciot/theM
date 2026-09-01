package middleware

import (
	"context"
	"encoding/json"
	"time"
)

// Job represents one row from them.middleware_jobs as claimed by a worker.
type Job struct {
	ID            string
	ArtifactID    string
	ApplicationID string
	RunID         string // may be empty
	SessionID     string // may be empty
	Processors    []string
	AttemptCount  int
	MaxAttempts   int

	// Artifact fields needed to load the part for processing
	FileName  string
	MimeType  string
	FileSize  int64
	FileBytes []byte // loaded separately by the worker
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

// Enqueue inserts a new pending job for the given artifact.
// Called inside the same transaction that saves the artifact.
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
  id::text, artifact_id::text, application_id::text,
  COALESCE(run_id::text,''), COALESCE(session_id::text,''),
  processors, attempt_count, max_attempts`

	var job Job
	var procs []string
	row := d.q.QueryRow(ctx, q)
	if err := row.Scan(
		&job.ID, &job.ArtifactID, &job.ApplicationID,
		&job.RunID, &job.SessionID,
		&procs, &job.AttemptCount, &job.MaxAttempts,
	); err != nil {
		return nil, nil // no rows = no work available
	}
	job.Processors = procs
	return &job, nil
}

// LoadFileBytes loads the file_bytes, file_name, mime_type, file_size for the
// artifact associated with job. Called after Claim.
func (d *JobDAL) LoadFileBytes(ctx context.Context, job *Job) error {
	const q = `
SELECT COALESCE(file_bytes, ''), COALESCE(file_name,''), COALESCE(mime_type,''), COALESCE(file_size,0)
FROM   them.artifacts
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

// Complete marks the job done and writes the result to both the job and the artifact.
// Clears file_bytes after a clean scan (bytes no longer needed).
func (d *JobDAL) Complete(ctx context.Context, job *Job, res JobResult) error {
	resultJSON, err := json.Marshal(res.Results)
	if err != nil {
		resultJSON = []byte("[]")
	}

	clearBytes := res.FinalStatus == "clean" || res.FinalStatus == "infected" || res.FinalStatus == "flagged"

	// Update artifact
	const updateArtifact = `
UPDATE them.artifacts
SET scan_status = $2,
    scan_result = $3::jsonb,
    scanned_at  = $4,
    file_bytes  = CASE WHEN $5 THEN NULL ELSE file_bytes END,
    updated_at  = now()
WHERE id = $1::uuid`
	if err := d.q.Exec(ctx, updateArtifact,
		job.ArtifactID, res.FinalStatus, resultJSON, res.ScannedAt, clearBytes,
	); err != nil {
		return err
	}

	// Update job
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
// The processor name is not on Result — callers must zip processors with results.
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
