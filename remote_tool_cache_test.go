package mcp

import (
	"fmt"
	"testing"
	"time"
)

func toolsNamed(names ...string) []MCPTool {
	out := make([]MCPTool, len(names))
	for i, n := range names {
		out[i] = MCPTool{Name: n}
	}
	return out
}

func TestRemoteToolCache_GetMissAndHit(t *testing.T) {
	c := newRemoteToolCache(10)
	now := time.Now()

	if _, ok := c.get("k", now); ok {
		t.Fatal("expected miss on empty cache")
	}

	c.put("k", toolsNamed("a", "b"), time.Minute, now)
	got, ok := c.get("k", now)
	if !ok || len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("expected hit with [a b], got ok=%v tools=%+v", ok, got)
	}
}

func TestRemoteToolCache_TTLExpiry(t *testing.T) {
	c := newRemoteToolCache(10)
	now := time.Now()
	c.put("k", toolsNamed("a"), time.Second, now)

	// Just before expiry: hit.
	if _, ok := c.get("k", now.Add(500*time.Millisecond)); !ok {
		t.Fatal("expected hit before expiry")
	}
	// At/after expiry: miss, and the entry is evicted.
	if _, ok := c.get("k", now.Add(time.Second)); ok {
		t.Fatal("expected miss at expiry")
	}
	if c.len() != 0 {
		t.Fatalf("expected expired entry evicted, len=%d", c.len())
	}
}

func TestRemoteToolCache_BoundedEviction(t *testing.T) {
	c := newRemoteToolCache(3)
	now := time.Now()

	for i := 0; i < 100; i++ {
		c.put(fmt.Sprintf("k%d", i), toolsNamed(fmt.Sprintf("t%d", i)), time.Hour, now)
	}
	if c.len() != 3 {
		t.Fatalf("expected cache bounded to 3, got %d", c.len())
	}
	// The three most-recently-inserted keys survive.
	for _, k := range []string{"k97", "k98", "k99"} {
		if _, ok := c.get(k, now); !ok {
			t.Fatalf("expected %s present", k)
		}
	}
	// An old key is gone.
	if _, ok := c.get("k0", now); ok {
		t.Fatal("expected k0 evicted")
	}
}

func TestRemoteToolCache_LRUOrdering(t *testing.T) {
	c := newRemoteToolCache(2)
	now := time.Now()

	c.put("a", toolsNamed("a"), time.Hour, now)
	c.put("b", toolsNamed("b"), time.Hour, now)

	// Touch "a" so it becomes most-recently-used; "b" is now the LRU.
	if _, ok := c.get("a", now); !ok {
		t.Fatal("expected a present")
	}

	// Inserting "c" should evict "b" (the LRU), not "a".
	c.put("c", toolsNamed("c"), time.Hour, now)

	if _, ok := c.get("b", now); ok {
		t.Fatal("expected b evicted as LRU")
	}
	if _, ok := c.get("a", now); !ok {
		t.Fatal("expected a retained")
	}
	if _, ok := c.get("c", now); !ok {
		t.Fatal("expected c present")
	}
}

func TestRemoteToolCache_PutUpdatesExisting(t *testing.T) {
	c := newRemoteToolCache(5)
	now := time.Now()

	c.put("k", toolsNamed("old"), time.Hour, now)
	c.put("k", toolsNamed("new1", "new2"), time.Hour, now)

	if c.len() != 1 {
		t.Fatalf("expected update not insert, len=%d", c.len())
	}
	got, ok := c.get("k", now)
	if !ok || len(got) != 2 || got[0].Name != "new1" {
		t.Fatalf("expected updated tools, got ok=%v tools=%+v", ok, got)
	}
}

func TestRemoteToolCache_InvalidateAndClear(t *testing.T) {
	c := newRemoteToolCache(5)
	now := time.Now()
	c.put("a", toolsNamed("a"), time.Hour, now)
	c.put("b", toolsNamed("b"), time.Hour, now)

	c.invalidate("a")
	if _, ok := c.get("a", now); ok {
		t.Fatal("expected a invalidated")
	}
	if _, ok := c.get("b", now); !ok {
		t.Fatal("expected b retained")
	}
	// Invalidating a missing key is a no-op.
	c.invalidate("missing")

	c.clear()
	if c.len() != 0 {
		t.Fatalf("expected empty after clear, len=%d", c.len())
	}
}

func TestRemoteToolCache_CopyIsolation(t *testing.T) {
	c := newRemoteToolCache(5)
	now := time.Now()

	src := toolsNamed("a")
	c.put("k", src, time.Hour, now)
	// Mutating the source after put must not affect the cached copy.
	src[0].Name = "mutated"

	got, _ := c.get("k", now)
	if got[0].Name != "a" {
		t.Fatalf("cache aliased the input slice: %+v", got)
	}
	// Mutating a returned copy must not affect the cache.
	got[0].Name = "changed"
	again, _ := c.get("k", now)
	if again[0].Name != "a" {
		t.Fatalf("cache aliased the returned slice: %+v", again)
	}
}

func TestRemoteToolCache_DefaultMaxWhenNonPositive(t *testing.T) {
	c := newRemoteToolCache(0)
	if c.max != DefaultRemoteToolCacheMaxEntries {
		t.Fatalf("expected default max, got %d", c.max)
	}
	c = newRemoteToolCache(-5)
	if c.max != DefaultRemoteToolCacheMaxEntries {
		t.Fatalf("expected default max for negative, got %d", c.max)
	}
}

func TestRemoteToolCache_ConcurrentAccess(t *testing.T) {
	c := newRemoteToolCache(64)
	now := time.Now()

	const workers = 16
	const iters = 500
	done := make(chan struct{})

	for w := 0; w < workers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < iters; i++ {
				key := fmt.Sprintf("k%d", (w*iters+i)%128)
				switch i % 4 {
				case 0:
					c.put(key, toolsNamed(key), time.Hour, now)
				case 1:
					c.get(key, time.Now())
				case 2:
					c.invalidate(key)
				case 3:
					_ = c.len()
				}
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		<-done
	}

	// Invariant: never exceeds the configured bound.
	if c.len() > 64 {
		t.Fatalf("cache exceeded bound under concurrency: %d", c.len())
	}
}
