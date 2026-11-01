// Package activitylog is a Temporal worker interceptor that logs every activity a worker runs: name,
// workflow ID (which carries the run ID), attempt, duration and the error if it failed.
//
// The workers used to log almost nothing (11 lines in 13 days on the dag workers), so a run's life
// could not be followed from `docker logs` (backlog F-132). One interceptor per worker covers every
// activity without touching each one.
package activitylog

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
)

// Interceptor logs activity executions. Activities named in quiet are logged at debug level only
// (high-volume ones such as per-node trace events).
type Interceptor struct {
	interceptor.WorkerInterceptorBase
	log   *slog.Logger
	quiet map[string]bool
}

// New returns an Interceptor logging to log. quiet lists activity names to keep at debug level.
func New(log *slog.Logger, quiet ...string) *Interceptor {
	q := make(map[string]bool, len(quiet))
	for _, n := range quiet {
		q[n] = true
	}
	return &Interceptor{log: log, quiet: q}
}

// InterceptActivity wraps the inbound activity interceptor chain.
func (i *Interceptor) InterceptActivity(ctx context.Context, next interceptor.ActivityInboundInterceptor) interceptor.ActivityInboundInterceptor {
	return &inbound{ActivityInboundInterceptorBase: interceptor.ActivityInboundInterceptorBase{Next: next}, i: i}
}

type inbound struct {
	interceptor.ActivityInboundInterceptorBase
	i *Interceptor
}

// ExecuteActivity runs the activity and logs how it went.
func (a *inbound) ExecuteActivity(ctx context.Context, in *interceptor.ExecuteActivityInput) (interface{}, error) {
	info := activity.GetInfo(ctx)
	start := time.Now()
	res, err := a.Next.ExecuteActivity(ctx, in)

	name := info.ActivityType.Name
	attrs := []any{
		"activity", name,
		"workflow_id", info.WorkflowExecution.ID,
		"attempt", info.Attempt,
		"duration_ms", time.Since(start).Milliseconds(),
	}
	switch {
	case err == nil:
		level := slog.LevelInfo
		if a.i.quiet[name] {
			level = slog.LevelDebug
		}
		a.i.log.Log(ctx, level, "activity done", attrs...)
	case errors.Is(err, context.Canceled) || temporal.IsCanceledError(err):
		a.i.log.Info("activity canceled", attrs...)
	default:
		a.i.log.Warn("activity failed", append(attrs, "error", err.Error())...)
	}
	return res, err
}
