//go:build integration

package events

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/rueidis"
)

// Producers cap the events stream (approximate MAXLEN) so a stopped event-writer cannot fill the
// noeviction events Redis (backlog F-128). Needs a Redis (REDIS_ADDR, default localhost:6379); uses DB 1.
func TestRedisSink_WriteBatch_CapsStreamLength(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}, SelectDB: 1, DisableCache: true})
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	t.Cleanup(client.Close)
	ctx := context.Background()
	evs := make([]Event, 800)

	length := func(key string) int64 {
		n, err := client.Do(ctx, client.B().Xlen().Key(key).Build()).AsInt64()
		if err != nil {
			t.Fatalf("XLEN: %v", err)
		}
		return n
	}

	capped := "test:events:capped:" + uuid.NewString()
	uncapped := "test:events:uncapped:" + uuid.NewString()
	t.Cleanup(func() {
		client.Do(context.Background(), client.B().Del().Key(capped, uncapped).Build())
	})

	small := NewRedisSink(client, capped, 10, 0)
	t.Cleanup(func() { small.cancel() })
	small.maxLen = "50" // approximate trimming works on whole stream nodes (100 entries), so use >100 writes
	if err := small.writeBatch(ctx, evs); err != nil {
		t.Fatalf("writeBatch capped: %v", err)
	}
	if n := length(capped); n >= 800 {
		t.Errorf("capped stream length = %d, want it trimmed well below 800", n)
	}

	def := NewRedisSink(client, uncapped, 10, 0)
	t.Cleanup(func() { def.cancel() })
	if def.maxLen != streamMaxLen {
		t.Fatalf("default cap = %q, want %q", def.maxLen, streamMaxLen)
	}
	if err := def.writeBatch(ctx, evs); err != nil {
		t.Fatalf("writeBatch default: %v", err)
	}
	if n := length(uncapped); n != 800 {
		t.Errorf("default-cap stream length = %d, want all 800 kept (cap is 200000)", n)
	}
}
