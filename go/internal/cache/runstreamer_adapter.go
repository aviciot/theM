package cache

import (
	"context"

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/runstream"
)

// RunStreamerRedisClient adapts a rueidis client to satisfy
// runstream.RedisStreamer (the Redis Streams read surface used by
// StreamFromRedis). It is distinct from RunStreamRedisClient, which implements
// the legacy Pub/Sub Subscriber interface.
type RunStreamerRedisClient struct {
	client rueidis.Client
}

// NewRunStreamerRedisClient wraps a rueidis client for stream reads.
func NewRunStreamerRedisClient(c rueidis.Client) *RunStreamerRedisClient {
	return &RunStreamerRedisClient{client: c}
}

// XRange returns entries in [start, stop] inclusive.
func (r *RunStreamerRedisClient) XRange(ctx context.Context, key, start, stop string) ([]runstream.StreamEntry, error) {
	cmd := r.client.B().Xrange().Key(key).Start(start).End(stop).Build()
	entries, err := r.client.Do(ctx, cmd).AsXRange()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, err
	}
	return convertXRange(entries), nil
}

// XRangeN returns at most count entries in [start, stop] inclusive.
func (r *RunStreamerRedisClient) XRangeN(ctx context.Context, key, start, stop string, count int64) ([]runstream.StreamEntry, error) {
	cmd := r.client.B().Xrange().Key(key).Start(start).End(stop).Count(count).Build()
	entries, err := r.client.Do(ctx, cmd).AsXRange()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil
		}
		return nil, err
	}
	return convertXRange(entries), nil
}

// XRead blocks (per args.Block) for entries after the given cursor(s).
// A block timeout with no data yields a Redis nil, which is normalised to an
// empty result with no error so the caller can loop and re-check context.
func (r *RunStreamerRedisClient) XRead(ctx context.Context, args runstream.XReadArgs) ([]runstream.StreamMessage, error) {
	// args.Streams is an alternating key,id list. Split into keys and ids for the
	// rueidis builder, which takes them as separate variadic groups.
	n := len(args.Streams) / 2
	keys := make([]string, 0, n)
	ids := make([]string, 0, n)
	for i := 0; i+1 < len(args.Streams); i += 2 {
		keys = append(keys, args.Streams[i])
		ids = append(ids, args.Streams[i+1])
	}

	builder := r.client.B().Xread()
	var streams rueidis.Completed
	if args.Block > 0 {
		streams = builder.Block(args.Block).Streams().Key(keys...).Id(ids...).Build()
	} else {
		streams = builder.Streams().Key(keys...).Id(ids...).Build()
	}

	res, err := r.client.Do(ctx, streams).AsXRead()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, nil // block timeout, no new entries
		}
		return nil, err
	}

	out := make([]runstream.StreamMessage, 0, len(res))
	for stream, entries := range res {
		out = append(out, runstream.StreamMessage{
			Stream:  stream,
			Entries: convertXRange(entries),
		})
	}
	return out, nil
}

// convertXRange maps rueidis XRangeEntry values into runstream.StreamEntry.
// FieldValues is map[string]string; we widen to map[string]interface{} to match
// the interface, which keeps the mock and real client interchangeable.
func convertXRange(entries []rueidis.XRangeEntry) []runstream.StreamEntry {
	out := make([]runstream.StreamEntry, 0, len(entries))
	for _, e := range entries {
		vals := make(map[string]interface{}, len(e.FieldValues))
		for k, v := range e.FieldValues {
			vals[k] = v
		}
		out = append(out, runstream.StreamEntry{ID: e.ID, Values: vals})
	}
	return out
}

// compile-time interface check.
var _ runstream.RedisStreamer = (*RunStreamerRedisClient)(nil)
