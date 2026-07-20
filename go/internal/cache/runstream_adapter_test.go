package cache_test

import (
	"testing"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/runstream"
)

// TestRunStreamRedisClient_ImplementsInterface is a compile-time check that
// *RunStreamRedisClient satisfies the runstream.Subscriber interface.
// If the interface or struct drift out of alignment, this test fails to compile.
func TestRunStreamRedisClient_ImplementsInterface(t *testing.T) {
	var _ runstream.Subscriber = (*cache.RunStreamRedisClient)(nil)
}
