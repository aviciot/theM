package cache

import (
	"context"
	"fmt"

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/runstream"
)

// RunStreamerWriterRedisClient implements runstream.StreamPublisher via rueidis.
// It appends entries to a Redis Stream using XADD with an approximate MAXLEN cap.
type RunStreamerWriterRedisClient struct {
	c rueidis.Client
}

// NewRunStreamerWriterRedisClient wraps a rueidis client for stream writes.
func NewRunStreamerWriterRedisClient(c rueidis.Client) *RunStreamerWriterRedisClient {
	return &RunStreamerWriterRedisClient{c: c}
}

// streamWriteMaxLen is the MAXLEN threshold for XADD, matching streamMaxLen on
// the read side so trimming behaviour is consistent across processes.
const streamWriteMaxLen = "5000"

// XAdd appends one entry to the named Redis Stream.
//
// The command issued is:
//
//	XADD <key> MAXLEN ~ 5000 * <field> <value> [<field> <value> ...]
//
// The approximate MAXLEN cap (~) keeps stream memory bounded without blocking
// the producer. It matches the cap used by the Python bridge.
func (r *RunStreamerWriterRedisClient) XAdd(ctx context.Context, key string, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return fmt.Errorf("runstream writer: XAdd called with empty fields map")
	}

	// Build XADD key MAXLEN ~ 5000 * <fields...>
	// rueidis builder chain: Xadd → Key → Maxlen → Almost → Threshold → Id → FieldValue → FieldValue(k,v)... → Build
	fvBuilder := r.c.B().Xadd().Key(key).Maxlen().Almost().Threshold(streamWriteMaxLen).Id("*").FieldValue()

	for k, v := range fields {
		var sv string
		switch tv := v.(type) {
		case string:
			sv = tv
		case []byte:
			sv = string(tv)
		default:
			sv = fmt.Sprintf("%v", tv)
		}
		fvBuilder = fvBuilder.FieldValue(k, sv)
	}

	cmd := fvBuilder.Build()
	return r.c.Do(ctx, cmd).Error()
}

// compile-time interface check.
var _ runstream.StreamPublisher = (*RunStreamerWriterRedisClient)(nil)
