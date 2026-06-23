package mcp

import (
	"container/list"
	"sync"
	"time"
)

// DefaultRemoteToolCacheMaxEntries bounds the number of distinct cache keys a
// RemoteProvider keeps tool lists for. It exists so a CacheKey that embeds a
// per-user/per-tenant identifier cannot grow the cache without limit. Override
// with WithMaxCacheEntries.
const DefaultRemoteToolCacheMaxEntries = 1024

// remoteToolCache is a bounded, TTL-aware LRU cache of remote tool lists.
// Entries expire by time (TTL) and are also evicted in least-recently-used
// order once the number of live entries exceeds the configured maximum, so the
// cache stays bounded regardless of how many distinct keys are seen.
type remoteToolCache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List // front = most recently used; Value is *remoteToolCacheEntry
	items map[string]*list.Element
}

type remoteToolCacheEntry struct {
	key       string
	tools     []MCPTool
	expiresAt time.Time
}

// newRemoteToolCache creates a cache holding at most max live entries.
// A max <= 0 uses DefaultRemoteToolCacheMaxEntries.
func newRemoteToolCache(max int) *remoteToolCache {
	if max <= 0 {
		max = DefaultRemoteToolCacheMaxEntries
	}
	return &remoteToolCache{
		max:   max,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}
}

// get returns a copy of the cached tools for key when present and not expired
// as of now. Expired entries are evicted on access. A hit moves the entry to
// the most-recently-used position.
func (c *remoteToolCache) get(key string, now time.Time) ([]MCPTool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*remoteToolCacheEntry)
	if !now.Before(entry.expiresAt) {
		c.removeElement(el)
		return nil, false
	}
	c.ll.MoveToFront(el)

	out := make([]MCPTool, len(entry.tools))
	copy(out, entry.tools)
	return out, true
}

// put stores a copy of tools under key, expiring ttl after now, evicting the
// least-recently-used entries when the cache exceeds its maximum size. Taking
// now (rather than reading the clock internally) keeps it symmetric with get
// and lets tests control expiry deterministically.
func (c *remoteToolCache) put(key string, tools []MCPTool, ttl time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stored := make([]MCPTool, len(tools))
	copy(stored, tools)
	expiresAt := now.Add(ttl)

	if el, ok := c.items[key]; ok {
		entry := el.Value.(*remoteToolCacheEntry)
		entry.tools = stored
		entry.expiresAt = expiresAt
		c.ll.MoveToFront(el)
		return
	}

	el := c.ll.PushFront(&remoteToolCacheEntry{key: key, tools: stored, expiresAt: expiresAt})
	c.items[key] = el

	for c.ll.Len() > c.max {
		c.removeOldest()
	}
}

// invalidate removes the entry for key, if present.
func (c *remoteToolCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}
}

// clear removes all entries.
func (c *remoteToolCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element)
}

// len reports the number of live entries (primarily for tests/metrics).
func (c *remoteToolCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *remoteToolCache) removeOldest() {
	if el := c.ll.Back(); el != nil {
		c.removeElement(el)
	}
}

// removeElement must be called with c.mu held.
func (c *remoteToolCache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	entry := el.Value.(*remoteToolCacheEntry)
	delete(c.items, entry.key)
}
