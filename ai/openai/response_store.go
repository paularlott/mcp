package openai

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrResponseNotFound is returned when a response doesn't exist, has expired,
// or belongs to a different owner.
var ErrResponseNotFound = errors.New("response not found")

// StoredResponse is the persisted record of an emulated response.
//
// Conversation history is stored once: each record holds only the messages
// its turn added (Turn), plus the IDs of the earlier responses in its
// conversation (Ancestors), so a conversation is rebuilt with one batched
// read however long it is.
type StoredResponse struct {
	ID                 string          `json:"id"`
	Owner              string          `json:"owner"`
	Model              string          `json:"model"`
	Status             ResponseStatus  `json:"status"`
	Result             *ResponseObject `json:"result,omitempty"`
	Error              string          `json:"error,omitempty"`
	CreatedAt          int64           `json:"created_at"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Ancestors          []string        `json:"ancestors,omitempty"` // earlier responses in the conversation, oldest first
	Turn               []Message       `json:"turn,omitempty"`
	ConversationBytes  int             `json:"conversation_bytes"` // encoded size of the conversation up to and including Turn
}

// ResponseStore persists emulated responses. Implementations must be safe for
// concurrent use. Records expire a store-chosen time after they were last
// saved or touched.
type ResponseStore interface {
	Save(ctx context.Context, rec *StoredResponse) error
	// Load returns ErrResponseNotFound if the record doesn't exist or expired.
	Load(ctx context.Context, id string) (*StoredResponse, error)
	// LoadMany returns the records in ids order, with nil for missing ones.
	LoadMany(ctx context.Context, ids []string) ([]*StoredResponse, error)
	Delete(ctx context.Context, id string) error
	// Touch restarts the expiry of the given records.
	Touch(ctx context.Context, ids ...string) error
	// RequestCancel asks the instance running an in-progress response to
	// cancel it; CancelRequested reports whether that was asked. A request
	// only matters while the response is in progress.
	RequestCancel(ctx context.Context, id string) error
	CancelRequested(ctx context.Context, id string) (bool, error)
}

// Defaults for MemoryResponseStore and KVResponseStore.
const (
	DefaultResponseTTL          = 15 * time.Minute
	DefaultMaxStoredResponses   = 10000
	DefaultMaxStoredBytes       = 256 << 20 // 256 MiB
	DefaultMaxConversationBytes = 8 << 20   // 8 MiB
)

// MemoryResponseStoreOptions configures a MemoryResponseStore. Zero values
// select the defaults; a negative value disables that limit.
type MemoryResponseStoreOptions struct {
	TTL          time.Duration // idle time after which a record expires
	MaxResponses int           // maximum number of records kept
	MaxBytes     int           // maximum total encoded size of records kept
}

// MemoryResponseStore is an in-process ResponseStore. When a limit is
// exceeded the least recently used records are evicted first; in-progress
// records are never evicted or expired. Records are stored encoded, so
// callers can't mutate stored state. Expired records can't be loaded, but
// are only removed from memory by later store operations.
type MemoryResponseStore struct {
	mu      sync.Mutex
	opts    MemoryResponseStoreOptions
	lru     *list.List // front = most recently used
	entries map[string]*list.Element
	cancels map[string]bool
	bytes   int
	now     func() time.Time
}

type memoryEntry struct {
	id         string
	data       []byte
	inProgress bool
	lastUsed   time.Time
}

// NewMemoryResponseStore creates an in-process response store.
func NewMemoryResponseStore(opts MemoryResponseStoreOptions) *MemoryResponseStore {
	if opts.TTL == 0 {
		opts.TTL = DefaultResponseTTL
	}
	if opts.MaxResponses == 0 {
		opts.MaxResponses = DefaultMaxStoredResponses
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = DefaultMaxStoredBytes
	}
	return &MemoryResponseStore{
		opts:    opts,
		lru:     list.New(),
		entries: make(map[string]*list.Element),
		cancels: make(map[string]bool),
		now:     time.Now,
	}
}

func (s *MemoryResponseStore) Save(ctx context.Context, rec *StoredResponse) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to encode response: %w", err)
	}
	if s.opts.MaxBytes > 0 && len(data) > s.opts.MaxBytes {
		return fmt.Errorf("response %s is %d bytes, over the store limit of %d", rec.ID, len(data), s.opts.MaxBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	entry := &memoryEntry{id: rec.ID, data: data, inProgress: isInProgress(rec.Status), lastUsed: now}
	if el, ok := s.entries[rec.ID]; ok {
		s.bytes -= len(el.Value.(*memoryEntry).data)
		el.Value = entry
		s.lru.MoveToFront(el)
	} else {
		s.entries[rec.ID] = s.lru.PushFront(entry)
	}
	s.bytes += len(data)
	if !entry.inProgress {
		delete(s.cancels, rec.ID)
	}
	s.evict(now)
	return nil
}

// evict removes expired records, then least recently used ones while over a
// limit. In-progress records are skipped. Caller holds s.mu.
func (s *MemoryResponseStore) evict(now time.Time) {
	overLimit := func() bool {
		return (s.opts.MaxResponses > 0 && s.lru.Len() > s.opts.MaxResponses) ||
			(s.opts.MaxBytes > 0 && s.bytes > s.opts.MaxBytes)
	}
	for el := s.lru.Back(); el != nil; {
		prev := el.Prev()
		entry := el.Value.(*memoryEntry)
		expired := s.opts.TTL > 0 && now.Sub(entry.lastUsed) > s.opts.TTL
		if !expired && !overLimit() {
			break // everything further forward was used more recently
		}
		if !entry.inProgress {
			s.remove(el)
		}
		el = prev
	}
}

func (s *MemoryResponseStore) remove(el *list.Element) {
	entry := el.Value.(*memoryEntry)
	s.lru.Remove(el)
	delete(s.entries, entry.id)
	delete(s.cancels, entry.id)
	s.bytes -= len(entry.data)
}

func (s *MemoryResponseStore) Load(ctx context.Context, id string) (*StoredResponse, error) {
	recs, err := s.LoadMany(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	if recs[0] == nil {
		return nil, ErrResponseNotFound
	}
	return recs[0], nil
}

func (s *MemoryResponseStore) LoadMany(ctx context.Context, ids []string) ([]*StoredResponse, error) {
	data := make([][]byte, len(ids))
	s.mu.Lock()
	now := s.now()
	for i, id := range ids {
		el, ok := s.entries[id]
		if !ok {
			continue
		}
		entry := el.Value.(*memoryEntry)
		if !entry.inProgress && s.opts.TTL > 0 && now.Sub(entry.lastUsed) > s.opts.TTL {
			s.remove(el)
			continue
		}
		data[i] = entry.data
	}
	s.mu.Unlock()

	recs := make([]*StoredResponse, len(ids))
	for i, d := range data {
		if d == nil {
			continue
		}
		var rec StoredResponse
		if err := json.Unmarshal(d, &rec); err != nil {
			return nil, fmt.Errorf("failed to decode response %s: %w", ids[i], err)
		}
		recs[i] = &rec
	}
	return recs, nil
}

func (s *MemoryResponseStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.entries[id]; ok {
		s.remove(el)
	}
	return nil
}

func (s *MemoryResponseStore) Touch(ctx context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, id := range ids {
		if el, ok := s.entries[id]; ok {
			el.Value.(*memoryEntry).lastUsed = now
			s.lru.MoveToFront(el)
		}
	}
	return nil
}

func (s *MemoryResponseStore) RequestCancel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.entries[id]; ok && el.Value.(*memoryEntry).inProgress {
		s.cancels[id] = true
	}
	return nil
}

func (s *MemoryResponseStore) CancelRequested(ctx context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancels[id], nil
}

// CancelPollInterval disables polling for cancel requests: everything using
// an in-process store runs in this process, where cancellation is direct.
func (s *MemoryResponseStore) CancelPollInterval() time.Duration { return -1 }

// Len returns the number of records held, including expired ones not yet
// removed.
func (s *MemoryResponseStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lru.Len()
}

// KeyValueStore is the minimal key-value interface KVResponseStore needs,
// e.g. a thin adapter over a Redis client:
//
//	type redisKV struct{ rdb *redis.Client }
//
//	func (r redisKV) Get(ctx context.Context, keys ...string) ([][]byte, error) {
//		vals, err := r.rdb.MGet(ctx, keys...).Result()
//		if err != nil {
//			return nil, err
//		}
//		out := make([][]byte, len(vals))
//		for i, v := range vals {
//			if s, ok := v.(string); ok {
//				out[i] = []byte(s)
//			}
//		}
//		return out, nil
//	}
//	func (r redisKV) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
//		return r.rdb.Set(ctx, key, value, ttl).Err()
//	}
//	func (r redisKV) Delete(ctx context.Context, keys ...string) error {
//		return r.rdb.Del(ctx, keys...).Err()
//	}
//	func (r redisKV) Expire(ctx context.Context, ttl time.Duration, keys ...string) error {
//		pipe := r.rdb.Pipeline()
//		for _, key := range keys {
//			pipe.Expire(ctx, key, ttl)
//		}
//		_, err := pipe.Exec(ctx)
//		return err
//	}
//
// Each method is called with at least one key, and should cost one round
// trip however many keys it is given.
type KeyValueStore interface {
	// Get returns the values in keys order, with nil for missing keys.
	Get(ctx context.Context, keys ...string) ([][]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	// Expire resets the keys' time to live; missing keys are not an error.
	Expire(ctx context.Context, ttl time.Duration, keys ...string) error
}

// KVResponseStoreOptions configures a KVResponseStore. Zero values select the
// defaults.
type KVResponseStoreOptions struct {
	KeyPrefix string        // prepended to response IDs (default "mcp:response:")
	TTL       time.Duration // time since last save or touch after which a record expires
	// InProgressTTL bounds how long an in-progress record lives, so a crashed
	// instance's responses don't stay in progress forever (default 1 hour,
	// and never less than TTL).
	InProgressTTL time.Duration
	// CancelPollInterval is how often each in-flight response checks the
	// store for a cancel or delete request from another instance (default
	// 1 second). Set it negative on a single instance to avoid the reads;
	// cancelling or deleting from another instance then fails.
	CancelPollInterval time.Duration
}

// KVResponseStore is a ResponseStore over a KeyValueStore such as Redis, for
// sharing responses between instances. Expiry uses the key-value store's
// TTLs; limit total size with the key-value store's own memory settings.
type KVResponseStore struct {
	kv   KeyValueStore
	opts KVResponseStoreOptions
}

// NewKVResponseStore creates a response store over a key-value store.
func NewKVResponseStore(kv KeyValueStore, opts KVResponseStoreOptions) *KVResponseStore {
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "mcp:response:"
	}
	if opts.TTL == 0 {
		opts.TTL = DefaultResponseTTL
	}
	if opts.InProgressTTL == 0 {
		opts.InProgressTTL = time.Hour
	}
	if opts.InProgressTTL < opts.TTL {
		opts.InProgressTTL = opts.TTL
	}
	return &KVResponseStore{kv: kv, opts: opts}
}

// CancelPollInterval returns KVResponseStoreOptions.CancelPollInterval.
func (s *KVResponseStore) CancelPollInterval() time.Duration { return s.opts.CancelPollInterval }

func (s *KVResponseStore) key(id string) string       { return s.opts.KeyPrefix + id }
func (s *KVResponseStore) cancelKey(id string) string { return s.opts.KeyPrefix + "cancel:" + id }

func (s *KVResponseStore) Save(ctx context.Context, rec *StoredResponse) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to encode response: %w", err)
	}
	ttl := s.opts.TTL
	if isInProgress(rec.Status) {
		ttl = s.opts.InProgressTTL
	}
	if err := s.kv.Set(ctx, s.key(rec.ID), data, ttl); err != nil {
		return fmt.Errorf("failed to save response %s: %w", rec.ID, err)
	}
	return nil
}

func (s *KVResponseStore) Load(ctx context.Context, id string) (*StoredResponse, error) {
	recs, err := s.LoadMany(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	if recs[0] == nil {
		return nil, ErrResponseNotFound
	}
	return recs[0], nil
}

func (s *KVResponseStore) LoadMany(ctx context.Context, ids []string) ([]*StoredResponse, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = s.key(id)
	}
	vals, err := s.kv.Get(ctx, keys...)
	if err != nil {
		return nil, fmt.Errorf("failed to load responses: %w", err)
	}
	if len(vals) != len(ids) {
		return nil, fmt.Errorf("failed to load responses: got %d values for %d keys", len(vals), len(ids))
	}
	recs := make([]*StoredResponse, len(ids))
	for i, data := range vals {
		if data == nil {
			continue
		}
		var rec StoredResponse
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("failed to decode response %s: %w", ids[i], err)
		}
		recs[i] = &rec
	}
	return recs, nil
}

func (s *KVResponseStore) Delete(ctx context.Context, id string) error {
	if err := s.kv.Delete(ctx, s.key(id), s.cancelKey(id)); err != nil {
		return fmt.Errorf("failed to delete response %s: %w", id, err)
	}
	return nil
}

func (s *KVResponseStore) Touch(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = s.key(id)
	}
	if err := s.kv.Expire(ctx, s.opts.TTL, keys...); err != nil {
		return fmt.Errorf("failed to touch responses: %w", err)
	}
	return nil
}

func (s *KVResponseStore) RequestCancel(ctx context.Context, id string) error {
	if err := s.kv.Set(ctx, s.cancelKey(id), []byte("1"), s.opts.InProgressTTL); err != nil {
		return fmt.Errorf("failed to request cancellation of response %s: %w", id, err)
	}
	return nil
}

func (s *KVResponseStore) CancelRequested(ctx context.Context, id string) (bool, error) {
	vals, err := s.kv.Get(ctx, s.cancelKey(id))
	if err != nil {
		return false, err
	}
	return len(vals) == 1 && vals[0] != nil, nil
}

func isInProgress(status ResponseStatus) bool {
	return status == StatusInProgress || status == StatusQueued
}
