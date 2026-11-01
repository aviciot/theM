package appflow

import "go.temporal.io/sdk/workflow"

// receiveOrCancel blocks until a value arrives on ch or the workflow is cancelled.
// A plain ch.Receive(ctx, ...) never wakes on cancellation (the SDK only observes ctx in
// Await, Selector and activity calls), so a flow waiting for a signal would run forever
// and its deferred FinalizeRunActivity would never fire (F-134). Returns ctx.Err()
// (a Temporal CanceledError) when cancelled. Deterministic: a Selector issues no commands.
func receiveOrCancel(ctx workflow.Context, ch workflow.ReceiveChannel, valuePtr interface{}) error {
	cancelled := false
	workflow.NewSelector(ctx).
		AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) { c.Receive(ctx, valuePtr) }).
		AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) { cancelled = true }).
		Select(ctx)
	if cancelled {
		return ctx.Err()
	}
	return nil
}
