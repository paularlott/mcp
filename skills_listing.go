package mcp

import (
	"context"
	"strings"
	"sync"
	"time"
)

// RemoteSkillsSource is one remote server whose skills feed a chat-side
// listing: Namespace labels its skills in prompt lines, Client is the
// connection to fetch through, and CacheKey overrides the cache identity
// (default: the namespace) for callers whose sources are scoped more
// finely — per user, say.
type RemoteSkillsSource struct {
	Namespace string
	Client    *Client
	CacheKey  string
}

// RemoteSkillsResult is one source's listing, namespace attached.
type RemoteSkillsResult struct {
	Namespace string
	Skills    []Skill
}

// SkillsListingCache caches per-source skills/list results and refreshes
// them in parallel, each fetch with its own budget. Built for chat-side
// prompt assembly (knot, llmrouter): a slow or dead remote is skipped
// without starving the others, and repeat prompts don't pay the round trips
// again within the TTL.
//
// The zero value is ready to use, with a one-minute TTL and a one-second
// per-source budget; NewSkillsListingCache overrides either. Failed fetches
// are not cached — the next listing retries them.
type SkillsListingCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	budget  time.Duration
	entries map[string]skillsListingEntry
}

type skillsListingEntry struct {
	skills  []Skill
	expires time.Time
}

// NewSkillsListingCache sets the listing TTL and the per-source fetch
// budget. Zero for either keeps that default (one minute, one second).
func NewSkillsListingCache(ttl, fetchBudget time.Duration) *SkillsListingCache {
	c := &SkillsListingCache{}
	if ttl > 0 {
		c.ttl = ttl
	}
	if fetchBudget > 0 {
		c.budget = fetchBudget
	}
	return c
}

// Listings returns every source's skills, fetching uncached sources in
// parallel, each with its own budget. Sources whose fetch fails are skipped
// after onErr (which may be nil) — one dead remote never blanks the rest.
// Results keep source order and are not re-sorted: callers that need
// deterministic output sort by namespace themselves.
func (c *SkillsListingCache) Listings(ctx context.Context, sources []RemoteSkillsSource, onErr func(namespace string, err error)) []RemoteSkillsResult {
	ttl, budget := c.ttl, c.budget
	if ttl == 0 {
		ttl = time.Minute
	}
	if budget == 0 {
		budget = time.Second
	}

	results := make([]RemoteSkillsResult, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		key := src.CacheKey
		if key == "" {
			key = src.Namespace
		}

		c.mu.Lock()
		entry, cached := c.entries[key]
		c.mu.Unlock()
		if cached && time.Now().Before(entry.expires) {
			results[i] = RemoteSkillsResult{Namespace: src.Namespace, Skills: entry.skills}
			continue
		}

		wg.Add(1)
		go func(i int, src RemoteSkillsSource, key string) {
			defer wg.Done()
			fetchCtx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			if err := src.Client.Initialize(fetchCtx); err != nil {
				if onErr != nil {
					onErr(src.Namespace, err)
				}
				return
			}
			skills, err := src.Client.ListSkills(fetchCtx)
			if err != nil {
				if onErr != nil {
					onErr(src.Namespace, err)
				}
				return
			}
			c.mu.Lock()
			if c.entries == nil {
				c.entries = make(map[string]skillsListingEntry)
			}
			c.entries[key] = skillsListingEntry{skills: skills, expires: time.Now().Add(ttl)}
			c.mu.Unlock()
			results[i] = RemoteSkillsResult{Namespace: src.Namespace, Skills: skills}
		}(i, src, key)
	}
	wg.Wait()
	return results
}

// Invalidate drops every cached listing (a source set that changed shape —
// servers added or removed — should not keep serving the old ones).
func (c *SkillsListingCache) Invalidate() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

// SkillPromptLine renders one skill as a prompt line for a chat model:
// "- namespace/name: description (uri)", or without the namespace prefix
// when empty. The name falls back to the URI path when the frontmatter
// lacks one. The description is what lets the model decide a skill is
// relevant before spending a read on it.
func SkillPromptLine(namespace string, skill Skill) string {
	name, _ := skill.Frontmatter["name"].(string)
	if name == "" {
		name = strings.TrimSuffix(strings.TrimPrefix(skill.URI, "skill://"), "/SKILL.md")
	}
	if namespace != "" {
		name = namespace + "/" + name
	}
	if description, _ := skill.Frontmatter["description"].(string); description != "" {
		return "- " + name + ": " + description + " (" + skill.URI + ")"
	}
	return "- " + name + " (" + skill.URI + ")"
}
