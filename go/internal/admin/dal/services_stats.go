package dal

import (
	"context"
	"time"
)

// SecurityScanStats holds aggregated security scanner metrics for a time window.
type SecurityScanStats struct {
	// Totals
	TotalArtifacts int64 `json:"total_artifacts"`
	Scanned        int64 `json:"scanned"`  // non-disabled
	Clean          int64 `json:"clean"`
	Infected       int64 `json:"infected"`
	Error          int64 `json:"error"`
	Pending        int64 `json:"pending"`
	Disabled       int64 `json:"disabled"`

	// Derived
	SuccessRate float64 `json:"success_rate"` // clean / scanned * 100

	// Latency (ms) from middleware_audit
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms"`

	// Quarantine health — rows still in quarantine_artifacts
	QuarantineTotal   int64 `json:"quarantine_total"`   // all rows (in-progress + expired)
	QuarantineExpired int64 `json:"quarantine_expired"` // rows past expires_at (reaper hasn't run yet)

	// Per-day trend (last N days)
	DailyTrend []DailyTrendRow `json:"daily_trend"`

	// Per-app breakdown
	AppBreakdown []AppScanRow `json:"app_breakdown"`

	// Recent jobs
	RecentJobs []RecentJobRow `json:"recent_jobs"`
}

type DailyTrendRow struct {
	Day      string `json:"day"` // YYYY-MM-DD
	Total    int64  `json:"total"`
	Clean    int64  `json:"clean"`
	Infected int64  `json:"infected"`
	Error    int64  `json:"error"`
}

type AppScanRow struct {
	AppID   string `json:"app_id"`
	AppSlug string `json:"app_slug"`
	Scanned int64  `json:"scanned"`
	Clean   int64  `json:"clean"`
	Error   int64  `json:"error"`
}

type RecentJobRow struct {
	JobID      string  `json:"job_id"`
	ArtifactID string  `json:"artifact_id"`
	Status     string  `json:"status"`
	Processor  string  `json:"processor"`
	Outcome    *string `json:"outcome"`
	DurationMs *int    `json:"duration_ms"`
	CreatedAt  string  `json:"created_at"`
}

// GetSecurityScanStats returns aggregated security scanner stats for the given window.
func GetSecurityScanStats(ctx context.Context, db Querier, since time.Time) (*SecurityScanStats, error) {
	s := &SecurityScanStats{}

	// ── Artifact totals ───────────────────────────────────────────────────────
	const totalsQ = `
SELECT
  count(*)                                                   AS total,
  count(*) FILTER (WHERE scan_status <> 'disabled')          AS scanned,
  count(*) FILTER (WHERE scan_status = 'clean')              AS clean,
  count(*) FILTER (WHERE scan_status = 'infected')           AS infected,
  count(*) FILTER (WHERE scan_status = 'error')              AS error,
  count(*) FILTER (WHERE scan_status = 'pending')            AS pending,
  count(*) FILTER (WHERE scan_status = 'disabled')           AS disabled
FROM them.run_artifacts
WHERE created_at >= $1`

	row := db.QueryRow(ctx, totalsQ, since)
	if err := row.Scan(&s.TotalArtifacts, &s.Scanned, &s.Clean, &s.Infected, &s.Error, &s.Pending, &s.Disabled); err != nil {
		return nil, err
	}
	if s.Scanned > 0 {
		s.SuccessRate = float64(s.Clean) / float64(s.Scanned) * 100
	}

	// ── Latency from middleware_audit ─────────────────────────────────────────
	const latencyQ = `
SELECT
  COALESCE(avg(duration_ms), 0),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms), 0)
FROM them.middleware_audit
WHERE created_at >= $1 AND duration_ms IS NOT NULL AND duration_ms > 0`

	lrow := db.QueryRow(ctx, latencyQ, since)
	if err := lrow.Scan(&s.AvgLatencyMs, &s.P95LatencyMs); err != nil {
		return nil, err
	}

	// ── Quarantine health ─────────────────────────────────────────────────────
	// Only count rows with bytes still in MinIO (storage_key IS NOT NULL).
	// Rows with storage_key=NULL are post-scan tombstones awaiting the reaper.
	const quarantineQ = `
SELECT
  count(*) FILTER (WHERE storage_key IS NOT NULL)                                   AS total,
  count(*) FILTER (WHERE storage_key IS NOT NULL AND expires_at < now())            AS expired
FROM them.quarantine_artifacts`

	qrow := db.QueryRow(ctx, quarantineQ)
	if err := qrow.Scan(&s.QuarantineTotal, &s.QuarantineExpired); err != nil {
		return nil, err
	}

	// ── Daily trend ───────────────────────────────────────────────────────────
	const trendQ = `
SELECT
  to_char(date_trunc('day', created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS day,
  count(*) FILTER (WHERE scan_status <> 'disabled')        AS total,
  count(*) FILTER (WHERE scan_status = 'clean')            AS clean,
  count(*) FILTER (WHERE scan_status = 'infected')         AS infected,
  count(*) FILTER (WHERE scan_status = 'error')            AS error
FROM them.run_artifacts
WHERE created_at >= $1
GROUP BY 1
ORDER BY 1`

	trows, err := db.Query(ctx, trendQ, since)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var r DailyTrendRow
		if err := trows.Scan(&r.Day, &r.Total, &r.Clean, &r.Infected, &r.Error); err != nil {
			return nil, err
		}
		s.DailyTrend = append(s.DailyTrend, r)
	}
	if s.DailyTrend == nil {
		s.DailyTrend = []DailyTrendRow{}
	}

	// ── Per-app breakdown ─────────────────────────────────────────────────────
	const appQ = `
SELECT
  a.application_id::text,
  COALESCE(ap.slug, a.application_id::text) AS app_slug,
  count(*)                                             AS scanned,
  count(*) FILTER (WHERE a.scan_status = 'clean')     AS clean,
  count(*) FILTER (WHERE a.scan_status = 'error')     AS error
FROM them.run_artifacts a
LEFT JOIN them.applications ap ON ap.id = a.application_id
WHERE a.created_at >= $1 AND a.scan_status <> 'disabled' AND a.application_id IS NOT NULL
GROUP BY 1, 2
ORDER BY scanned DESC
LIMIT 20`

	arows, err := db.Query(ctx, appQ, since)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var r AppScanRow
		if err := arows.Scan(&r.AppID, &r.AppSlug, &r.Scanned, &r.Clean, &r.Error); err != nil {
			return nil, err
		}
		s.AppBreakdown = append(s.AppBreakdown, r)
	}
	if s.AppBreakdown == nil {
		s.AppBreakdown = []AppScanRow{}
	}

	// ── Recent jobs ───────────────────────────────────────────────────────────
	// Use run_artifacts.scan_status as the display outcome — it is the
	// authoritative result written by the middleware worker after scanning.
	// middleware_audit.outcome records internal processor steps and may differ.
	const recentQ = `
SELECT
  j.id::text,
  j.artifact_id::text,
  j.status,
  COALESCE(j.processors[1], '') AS processor,
  ra.scan_status,
  (SELECT ma.duration_ms FROM them.middleware_audit ma WHERE ma.artifact_id = j.artifact_id ORDER BY ma.created_at DESC LIMIT 1),
  to_char(j.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at
FROM them.middleware_jobs j
LEFT JOIN them.run_artifacts ra ON ra.id = j.artifact_id
ORDER BY j.created_at DESC
LIMIT 20`

	jrows, err := db.Query(ctx, recentQ)
	if err != nil {
		return nil, err
	}
	defer jrows.Close()
	for jrows.Next() {
		var r RecentJobRow
		if err := jrows.Scan(&r.JobID, &r.ArtifactID, &r.Status, &r.Processor, &r.Outcome, &r.DurationMs, &r.CreatedAt); err != nil {
			return nil, err
		}
		s.RecentJobs = append(s.RecentJobs, r)
	}
	if s.RecentJobs == nil {
		s.RecentJobs = []RecentJobRow{}
	}

	return s, nil
}
