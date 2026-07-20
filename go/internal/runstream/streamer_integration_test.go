//go:build integration

// Integration tests for the Redis Streams reader. Requires a live Redis
// reachable at REDIS_ADDR (default localhost:6379). Run with:
//
//	go test -tags=integration ./internal/runstream/...
package runstream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/runstream"
)

func redisAddr() string {
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		return v
	}
	return "localhost:6379"
}

// xadd appends one JSON event to the run stream via a raw rueidis XADD.
func xadd(t *testing.T, rc rueidis.Client, key, evType string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"type": evType, "text": "hi"})
	cmd := rc.B().Xadd().Key(key).Id("*").FieldValue().FieldValue("data", string(payload)).Build()
	require.NoError(t, rc.Do(context.Background(), cmd).Error())
}

func TestIntegration_StreamFromRedis_ReplayThenTerminal(t *testing.T) {
	rc, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress:  []string{redisAddr()},
		DisableCache: true,
	})
	require.NoError(t, err)
	defer rc.Close()

	runID := fmt.Sprintf("itest-%d", time.Now().UnixNano())
	key := fmt.Sprintf("them:dash:run:%s:stream", runID)
	defer rc.Do(context.Background(), rc.B().Del().Key(key).Build())

	// Write 3 events before the reader starts (replay path).
	xadd(t, rc, key, "token")
	xadd(t, rc, key, "token")
	xadd(t, rc, key, "token")

	streamer := cache.NewRunStreamerRedisClient(rc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runstream.StreamFromRedis(ctx, streamer, runID, runstream.StreamerOptions{})
	require.NoError(t, err)

	// Collect the 3 replayed tokens.
	var got []string
	timeout := time.After(3 * time.Second)
	for len(got) < 3 {
		select {
		case ev := <-out:
			got = append(got, ev.Type)
		case <-timeout:
			t.Fatalf("timed out; got %v", got)
		}
	}
	assert.Equal(t, []string{"token", "token", "token"}, got)

	// Now write a terminal event live; the channel must deliver it and close.
	xadd(t, rc, key, "done")

	var last event.Event
	select {
	case ev, ok := <-out:
		require.True(t, ok)
		last = ev
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive live done event")
	}
	assert.Equal(t, "done", last.Type)

	// Channel closes after terminal.
	select {
	case _, ok := <-out:
		assert.False(t, ok, "channel must close after terminal event")
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after terminal event")
	}
}
