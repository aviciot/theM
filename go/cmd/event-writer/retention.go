package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/rueidis"
)

// RetentionManager safely trims ACKed events from Redis Streams while protecting
// pending and unread messages. Runs periodically in the background.
//
// Safety invariants:
// - Never trim pending messages (claimed but not ACKed)
// - Never trim unread messages (not yet claimed by any consumer)
// - Only trim when ALL consumers in the group have ACKed up to a point
type RetentionManager struct {
	redis         rueidis.Client
	logger        *slog.Logger
	streamKey     string
	dlqStreamKey  string
	consumerGroup string
	trimInterval  time.Duration
	maxAge        time.Duration // Max age of ACKed events before trim
	stopChan      chan struct{}
}

// NewRetentionManager creates a retention manager.
// maxAge: events older than this are trimmed (if ACKed by all consumers)
// trimInterval: how often to run trim operation
func NewRetentionManager(
	redis rueidis.Client,
	logger *slog.Logger,
	streamKey, dlqStreamKey, consumerGroup string,
	maxAge, trimInterval time.Duration,
) *RetentionManager {
	return &RetentionManager{
		redis:         redis,
		logger:        logger,
		streamKey:     streamKey,
		dlqStreamKey:  dlqStreamKey,
		consumerGroup: consumerGroup,
		maxAge:        maxAge,
		trimInterval:  trimInterval,
		stopChan:      make(chan struct{}),
	}
}

// Start begins the retention loop in the background.
func (r *RetentionManager) Start() {
	go r.run()
}

// Stop stops the retention loop.
func (r *RetentionManager) Stop() {
	close(r.stopChan)
}

func (r *RetentionManager) run() {
	ticker := time.NewTicker(r.trimInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopChan:
			return
		case <-ticker.C:
			if err := r.trimSafe(context.Background()); err != nil {
				r.logger.Error("retention trim failed", "error", err)
			}
		}
	}
}

// trimSafe safely trims the main stream and DLQ.
func (r *RetentionManager) trimSafe(ctx context.Context) error {
	// Trim main stream only up to safe point
	trimmed, err := r.trimStream(ctx, r.streamKey, r.consumerGroup)
	if err != nil {
		return fmt.Errorf("trim main stream: %w", err)
	}
	if trimmed > 0 {
		r.logger.Info("trimmed events from main stream", "count", trimmed, "stream", r.streamKey)
	}

	// Trim DLQ (no consumer group, age-based only)
	dlqTrimmed, err := r.trimDLQByAge(ctx)
	if err != nil {
		return fmt.Errorf("trim DLQ: %w", err)
	}
	if dlqTrimmed > 0 {
		r.logger.Info("trimmed events from DLQ", "count", dlqTrimmed, "stream", r.dlqStreamKey)
	}

	return nil
}

// trimStream safely trims a stream up to the point where ALL consumers have ACKed.
// Safety: protects BOTH pending (claimed but not ACKed) AND unread (not claimed yet).
func (r *RetentionManager) trimStream(ctx context.Context, stream, group string) (int, error) {
	// Check 1: Any pending messages?
	// XPENDING returns an array. Empty array = no pending, non-empty = has pending.
	pendingCmd := r.redis.B().Xpending().Key(stream).Group(group).Start("-").End("+").Count(1).Build()
	pendingArr, err := r.redis.Do(ctx, pendingCmd).ToArray()
	if err != nil {
		return 0, fmt.Errorf("XPENDING failed: %w", err)
	}
	if len(pendingArr) > 0 {
		r.logger.Debug("skipping trim: pending messages exist", "stream", stream, "count", len(pendingArr))
		return 0, nil
	}

	// Check 2: Get last-delivered-id from group info
	groupCmd := r.redis.B().XinfoGroups().Key(stream).Build()
	groupsResp, err := r.redis.Do(ctx, groupCmd).ToArray()
	if err != nil {
		return 0, fmt.Errorf("XINFO GROUPS failed: %w", err)
	}

	// Parse group info: XINFO GROUPS returns array of arrays
	// Each group: [name <name> consumers <N> pending <P> last-delivered-id <id> ...]
	var lastDeliveredID string
	var consumersCount int64
	for _, groupData := range groupsResp {
		groupMap, err := groupData.AsMap()
		if err != nil {
			continue
		}
		// Check if this is our group
		if nameMsg, ok := groupMap["name"]; ok {
			name, _ := nameMsg.ToString()
			if name == group {
				// Extract last-delivered-id
				if ldMsg, ok := groupMap["last-delivered-id"]; ok {
					lastDeliveredID, _ = ldMsg.ToString()
				}
				// Extract consumers count
				if cMsg, ok := groupMap["consumers"]; ok {
					consumersCount, _ = cMsg.AsInt64()
				}
				break
			}
		}
	}

	// Safety checks
	if consumersCount == 0 {
		r.logger.Debug("skipping trim: no active consumers", "stream", stream, "group", group)
		return 0, nil
	}

	if lastDeliveredID == "" || lastDeliveredID == "0-0" {
		r.logger.Debug("skipping trim: no messages delivered yet", "stream", stream, "group", group)
		return 0, nil
	}

	// Safe to trim: only messages OLDER than last-delivered-id AND older than maxAge
	return r.trimUpTo(ctx, stream, lastDeliveredID)
}

// trimUpTo trims events older than BOTH last-delivered-id AND maxAge (whichever is earlier).
// Protects unread messages by never trimming past what the consumer group has delivered.
func (r *RetentionManager) trimUpTo(ctx context.Context, stream, maxID string) (int, error) {
	// Parse maxID to get timestamp
	parts := strings.Split(maxID, "-")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid message ID: %s", maxID)
	}

	deliveredMs, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse message ID timestamp: %w", err)
	}

	// Apply maxAge limit
	cutoff := time.Now().Add(-r.maxAge)
	cutoffMs := cutoff.UnixMilli()

	// Use earlier of: delivered timestamp or age cutoff
	trimMs := deliveredMs
	if cutoffMs < deliveredMs {
		trimMs = cutoffMs
	}

	trimID := fmt.Sprintf("%d-0", trimMs)

	// Count before
	beforeCmd := r.redis.B().Xlen().Key(stream).Build()
	beforeLen, err := r.redis.Do(ctx, beforeCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("XLEN before: %w", err)
	}

	// Trim
	trimCmd := r.redis.B().Xtrim().Key(stream).Minid().Threshold(trimID).Build()
	if err := r.redis.Do(ctx, trimCmd).Error(); err != nil {
		return 0, fmt.Errorf("XTRIM: %w", err)
	}

	// Count after
	afterCmd := r.redis.B().Xlen().Key(stream).Build()
	afterLen, err := r.redis.Do(ctx, afterCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("XLEN after: %w", err)
	}

	return int(beforeLen - afterLen), nil
}

// trimToNow trims events older than maxAge (DEPRECATED - kept for reference).
// This method is no longer used because it doesn't protect unread messages.
func (r *RetentionManager) trimToNow(ctx context.Context, stream string) (int, error) {
	// Calculate cutoff timestamp
	cutoff := time.Now().Add(-r.maxAge)
	cutoffMs := cutoff.UnixMilli()
	cutoffID := fmt.Sprintf("%d-0", cutoffMs)

	// Count messages before trim
	beforeCmd := r.redis.B().Xlen().Key(stream).Build()
	beforeLen, err := r.redis.Do(ctx, beforeCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("XLEN before: %w", err)
	}

	// XTRIM <stream> MINID <cutoff-id>
	trimCmd := r.redis.B().Xtrim().Key(stream).Minid().Threshold(cutoffID).Build()
	if err := r.redis.Do(ctx, trimCmd).Error(); err != nil {
		return 0, fmt.Errorf("XTRIM: %w", err)
	}

	// Count after
	afterCmd := r.redis.B().Xlen().Key(stream).Build()
	afterLen, err := r.redis.Do(ctx, afterCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("XLEN after: %w", err)
	}

	trimmed := int(beforeLen - afterLen)
	return trimmed, nil
}

// trimDLQByAge trims DLQ based on age only (no consumer group).
func (r *RetentionManager) trimDLQByAge(ctx context.Context) (int, error) {
	// DLQ retention: keep for 7 days (longer than main stream for audit purposes)
	dlqMaxAge := 7 * 24 * time.Hour
	cutoff := time.Now().Add(-dlqMaxAge)
	cutoffMs := cutoff.UnixMilli()
	cutoffID := fmt.Sprintf("%d-0", cutoffMs)

	// Count before
	beforeCmd := r.redis.B().Xlen().Key(r.dlqStreamKey).Build()
	beforeLen, err := r.redis.Do(ctx, beforeCmd).AsInt64()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return 0, nil // DLQ doesn't exist yet
		}
		return 0, fmt.Errorf("DLQ XLEN before: %w", err)
	}

	// XTRIM DLQ MINID <cutoff-id>
	trimCmd := r.redis.B().Xtrim().Key(r.dlqStreamKey).Minid().Threshold(cutoffID).Build()
	if err := r.redis.Do(ctx, trimCmd).Error(); err != nil {
		return 0, fmt.Errorf("DLQ XTRIM: %w", err)
	}

	// Count after
	afterCmd := r.redis.B().Xlen().Key(r.dlqStreamKey).Build()
	afterLen, err := r.redis.Do(ctx, afterCmd).AsInt64()
	if err != nil {
		return 0, fmt.Errorf("DLQ XLEN after: %w", err)
	}

	trimmed := int(beforeLen - afterLen)
	return trimmed, nil
}
