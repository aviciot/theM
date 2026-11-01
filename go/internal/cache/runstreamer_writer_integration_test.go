//go:build integration

package cache_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/cache"
)

// XAdd must leave the run stream with an expiry (it used to live forever), and refresh it on every event.
// Needs a Redis (REDIS_ADDR, default localhost:6379).
func TestRunStreamerWriter_XAdd_SetsAndRefreshesTTL(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}, DisableCache: true})
	if err != nil {
		t.Fatalf("redis: %v", err)
	}
	t.Cleanup(client.Close)
	ctx := context.Background()
	key := "them:dash:run:inttest-" + uuid.NewString() + ":stream"
	t.Cleanup(func() { client.Do(context.Background(), client.B().Del().Key(key).Build()) })

	w := cache.NewRunStreamerWriterRedisClient(client)
	if err := w.XAdd(ctx, key, map[string]interface{}{"data": `{"type":"token"}`}); err != nil {
		t.Fatalf("XAdd: %v", err)
	}
	ttl, err := client.Do(ctx, client.B().Ttl().Key(key).Build()).AsInt64()
	if err != nil || ttl <= 0 || ttl > 86400 {
		t.Fatalf("ttl = %d (err %v), want 1..86400 — run streams must expire", ttl, err)
	}

	// shorten it, write again: the refresh pushes it back up
	client.Do(ctx, client.B().Expire().Key(key).Seconds(60).Build())
	if err := w.XAdd(ctx, key, map[string]interface{}{"data": `{"type":"done"}`}); err != nil {
		t.Fatalf("XAdd 2: %v", err)
	}
	ttl, _ = client.Do(ctx, client.B().Ttl().Key(key).Build()).AsInt64()
	if ttl <= 60 {
		t.Errorf("ttl after a second event = %d, want it refreshed above 60", ttl)
	}
	n, _ := client.Do(ctx, client.B().Xlen().Key(key).Build()).AsInt64()
	if n != 2 {
		t.Errorf("stream length = %d, want 2", n)
	}
}
