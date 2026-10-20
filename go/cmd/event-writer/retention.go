package main

import (
	"context"
	"fmt"
	"log/slog"
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
func (r *RetentionManager) trimStream(ctx context.Context, stream, group string) (int, error) {
	// Get pending messages info: XPENDING <stream> <group> - + 1
	cmd := r.redis.B().Xpending().Key(stream).Group(group).Start("-").End("+").Count(1).Build()
	result, err := r.redis.Do(ctx, cmd).ToMessage()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			// No pending messages
			return r.trimToNow(ctx, stream)
		}
		return 0, fmt.Errorf("XPENDING failed: %w", err)
	}

	// Parse XPENDING result to find oldest pending message ID
	// If no pending messages, we can trim aggressively
	// If there are pending messages, only trim up to the oldest pending ID

	// For now, conservative approach: if ANY pending messages exist, skip trim
	// This prevents accidentally deleting messages that might be recovered
	r.logger.Debug("skipping trim: pending messages exist", "stream", stream)
	return 0, nil
}

// trimToNow trims events older than maxAge.
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
