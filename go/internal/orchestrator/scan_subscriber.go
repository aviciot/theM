package orchestrator

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/rueidis"
)

// RueidisClient is the minimal interface the RedisScanSubscriber needs.
type RueidisClient interface {
	Receive(ctx context.Context, subscribe rueidis.Completed, fn func(msg rueidis.PubSubMessage)) error
	B() rueidis.Builder
}

// RedisScanSubscriber implements ScanSubscriber by subscribing to the
// them:run:<runID> Redis pub/sub channel and waiting for an
// artifact_scan_result event matching the given artifact ID.
type RedisScanSubscriber struct {
	client RueidisClient
}

// NewRedisScanSubscriber creates a RedisScanSubscriber backed by the given rueidis client.
func NewRedisScanSubscriber(client RueidisClient) *RedisScanSubscriber {
	return &RedisScanSubscriber{client: client}
}

// WaitForScanResult blocks until the middleware worker publishes an
// artifact_scan_result event for the given (runID, artifactID) pair.
// Returns (result, true) on success, (zero, false) on context cancellation or timeout.
func (s *RedisScanSubscriber) WaitForScanResult(ctx context.Context, runID, artifactID string, timeout time.Duration) (ScanResult, bool) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resultCh := make(chan ScanResult, 1)

	channel := "them:run:" + runID
	cmd := s.client.B().Subscribe().Channel(channel).Build()

	go func() {
		_ = s.client.Receive(waitCtx, cmd, func(msg rueidis.PubSubMessage) {
			var evt struct {
				Type          string `json:"type"`
				ArtifactID    string `json:"artifact_id"`
				ScanStatus    string `json:"scan_status"`
				Threat        string `json:"threat"`
				ArtifactName  string `json:"artifact_name"`
				FileSizeBytes int64  `json:"file_size_bytes"`
			}
			if err := json.Unmarshal([]byte(msg.Message), &evt); err != nil {
				return
			}
			if evt.Type != "artifact_scan_result" || evt.ArtifactID != artifactID {
				return
			}
			select {
			case resultCh <- ScanResult{
				ArtifactID:    evt.ArtifactID,
				ScanStatus:    evt.ScanStatus,
				Threat:        evt.Threat,
				FileName:      evt.ArtifactName,
				FileSizeBytes: evt.FileSizeBytes,
			}:
			default:
			}
			cancel() // stop Receive loop once we have the result
		})
	}()

	select {
	case res := <-resultCh:
		return res, true
	case <-waitCtx.Done():
		return ScanResult{}, false
	}
}
