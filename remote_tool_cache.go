package mcp

import (
	"container/list"
	"sync"
	"time"
)

// DefaultRemoteToolCacheMaxEntries bounds the number of distinct cache keys a
// RemoteProvider keeps remote listings (tool lists, skills lists) for. It
// exists so a CacheKey that embeds a per-user/per-tenant identifier cannot
// grow the cache without limit. Override with WithMaxCacheEntries.
const DefaultRemoteToolCacheMaxEntries = 1024

// remoteListCache is a bounded, TTL-aware LRU cache of remote listings.
// Entries expire by time (TTL) and are also evicted in least-recently-used
// order once the number of live entries exceeds the configured maximum, so
// the cache stays bounded regardless of how many distinct keys are seen.
type remoteListCache[T any] struct {
	mu    sync.Mutex
	max   int
	ll    *list.List // front = most recently used; Value is *remoteListCacheEntry[T]
	items map[string]*list.Element
}

type remoteListCacheEntry[T any] struct {
	key       string
	value     []T
	expiresAt time.Time
}

// newRemoteListCache creates a cache holding at most max live entries.
// A max <= 0 uses DefaultRemoteToolCacheMaxEntries.
func newRemoteListCache[T any](max int) *remoteListCache[T] {
	if max <= 0 {
		max = DefaultRemoteToolCacheMaxEntries
	}
	return &remoteListCache[T]{
		max:   max,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}
}

// get returns a copy of the cached values for key when present and not
// expired as of now. Expired entries are evicted on access. A hit moves the
// entry to the most-recently-used position.
func (c *remoteListCache[T]) get(key string, now time.Time) ([]T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*remoteListCacheEntry[T])
	if !now.Before(entry.expiresAt) {
		c.removeElement(el)
		return nil, false
	}
	c.ll.MoveToFront(el)

	out := make([]T, len(entry.value))
	copy(out, entry.value)
	return out, true
}

// put stores a copy of value under key, expiring ttl after now, evicting the
// least-recently-used entries when the cache exceeds its maximum size. Taking
// now (rather than reading the clock internally) keeps it symmetric with get
// and lets tests control expiry deterministically.
func (c *remoteListCache[T]) put(key string, value []T, ttl time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stored := make([]T, len(value))
	copy(stored, value)
	expiresAt := now.Add(ttl)

	if el, ok := c.items[key]; ok {
		entry := el.Value.(*remoteListCacheEntry[T])
		entry.value = stored
		entry.expiresAt = expiresAt
		c.ll.MoveToFront(el)
		return
	}

	el := c.ll.PushFront(&remoteListCacheEntry[T]{key: key, value: stored, expiresAt: expiresAt})
	c.items[key] = el

	for c.ll.Len() > c.max {
		c.removeOldest()
	}
}

// invalidate removes the entry for key, if present.
func (c *remoteListCache[T]) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}
}

// clear removes all entries.
func (c *remoteListCache[T]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element)
}

// len reports the number of live entries (primarily for tests/metrics).
func (c *remoteListCache[T]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *remoteListCache[T]) removeOldest() {
	if el := c.ll.Back(); el != nil {
		c.removeElement(el)
	}
}

// removeElement must be called with c.mu held.
func (c *remoteListCache[T]) removeElement(el *list.Element) {
	c.ll.Remove(el)
	entry := el.Value.(*remoteListCacheEntry[T])
	delete(c.items, entry.key)
}

// The tool and skills listings share the cache mechanics.
type remoteToolCache = remoteListCache[MCPTool]
type remoteSkillsCache = remoteListCache[Skill]

func newRemoteToolCache(max int) *remoteToolCache {
	return newRemoteListCache[MCPTool](max)
}

func newRemoteSkillsCache(max int) *remoteSkillsCache {
	return newRemoteListCache[Skill](max)
}
