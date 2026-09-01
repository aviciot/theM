package middleware

import (
	"context"
	"encoding/json"
	"time"
)

// RedisPublisher is the interface the worker uses to publish progress and final
// results. Implemented by the rueidis-backed publisher; a no-op version is used
// in tests.
type RedisPublisher interface {
	// Publish publishes a raw JSON payload to the given Redis channel.
	Publish(ctx context.Context, channel string, payload []byte) error
}

// ScanPublisher wraps a RedisPublisher and provides typed helpers for the
// middleware pipeline events.
type ScanPublisher struct {
	redis      RedisPublisher
	artifactID string
	runID      string
}

// NewScanPublisher creates a ScanPublisher.
func NewScanPublisher(redis RedisPublisher, artifactID, runID string) *ScanPublisher {
	return &ScanPublisher{redis: redis, artifactID: artifactID, runID: runID}
}

// PublishProgress implements ProgressPublisher. Publishes a per-processor
// progress event on them:scan:<artifactID>.
func (p *ScanPublisher) PublishProgress(ctx context.Context, processor, status string, detail map[string]any, durationMS int64) {
	payload, err := json.Marshal(map[string]any{
		"type":         "scan_progress",
		"artifact_id":  p.artifactID,
		"processor":    processor,
		"status":       status,
		"duration_ms":  durationMS,
		"detail":       detail,
	})
	if err != nil {
		return
	}
	_ = p.redis.Publish(ctx, "them:scan:"+p.artifactID, payload)
}

// PublishFinalResult publishes the completed pipeline result on them:run:<runID>
// (the existing run channel that Monitor already subscribes to) and also sends
// a final summary on them:scan:<artifactID> so the Monitor scan row can close.
func (p *ScanPublisher) PublishFinalResult(ctx context.Context, res JobResult, fileName string, fileSizeBytes int64) {
	type procSummary struct {
		Name       string         `json:"name"`
		Outcome    string         `json:"outcome"`
		DurationMS int64          `json:"duration_ms"`
		Detail     map[string]any `json:"detail,omitempty"`
	}
	procs := make([]procSummary, 0, len(res.Results))
	for _, r := range res.Results {
		procs = append(procs, procSummary{
			Outcome:    r.Outcome,
			DurationMS: r.DurationMS,
			Detail:     r.Detail,
		})
	}

	evt := map[string]any{
		"type":              "artifact_scan_result",
		"artifact_id":       p.artifactID,
		"artifact_name":     fileName,
		"file_size_bytes":   fileSizeBytes,
		"scan_status":       res.FinalStatus,
		"processors":        procs,
		"threat":            res.Threat,
		"total_duration_ms": res.TotalMS,
		"scanned_at":        time.Now().UTC().Format(time.RFC3339),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		return
	}

	// Final summary on the per-artifact scan channel (closes the Monitor scan row)
	_ = p.redis.Publish(ctx, "them:scan:"+p.artifactID, payload)

	// Final result on the run channel (appears in Monitor run feed)
	if p.runID != "" {
		_ = p.redis.Publish(ctx, "them:run:"+p.runID, payload)
	}
}

// NoopPublisher is a ProgressPublisher that does nothing. Used in tests.
type NoopPublisher struct{}

func (NoopPublisher) PublishProgress(_ context.Context, _, _ string, _ map[string]any, _ int64) {}
