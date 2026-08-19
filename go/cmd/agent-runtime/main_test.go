package main

import (
	"testing"
	"time"

	"github.com/aviciot/them/internal/agentgen"
)

// TestSpecCache_MissAndHit verifies TTL-based eviction and cache hit.
func TestSpecCache_MissAndHit(t *testing.T) {
	c := &specCache{entries: make(map[string]*cachedSpec)}

	// Cold miss.
	if got := c.get("abc"); got != nil {
		t.Fatal("expected nil on cold miss")
	}

	spec := &agentgen.AgentSpec{Slug: "test-slug"}
	c.set("abc", spec)

	// Hot hit.
	if got := c.get("abc"); got == nil {
		t.Fatal("expected spec after set")
	}

	// Expired entry returns nil.
	c.mu.Lock()
	c.entries["abc"].expiresAt = time.Now().Add(-1 * time.Second)
	c.mu.Unlock()
	if got := c.get("abc"); got != nil {
		t.Fatal("expected nil after TTL expiry")
	}
}

// TestSpecCache_IsolatedKeys verifies that different keys don't collide.
func TestSpecCache_IsolatedKeys(t *testing.T) {
	c := &specCache{entries: make(map[string]*cachedSpec)}

	s1 := &agentgen.AgentSpec{Slug: "agent-one"}
	s2 := &agentgen.AgentSpec{Slug: "agent-two"}
	c.set("id-1", s1)
	c.set("id-2", s2)

	got1 := c.get("id-1")
	got2 := c.get("id-2")
	if got1 == nil || got1.Slug != "agent-one" {
		t.Errorf("id-1: want agent-one, got %v", got1)
	}
	if got2 == nil || got2.Slug != "agent-two" {
		t.Errorf("id-2: want agent-two, got %v", got2)
	}
}
