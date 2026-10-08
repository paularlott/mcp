package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ResponseStatus represents the status of an async response
type ResponseStatus string

const (
	StatusQueued     ResponseStatus = "queued"
	StatusInProgress ResponseStatus = "in_progress"
	StatusCompleted  ResponseStatus = "completed"
	StatusFailed     ResponseStatus = "failed"
	StatusCancelled  ResponseStatus = "cancelled"
)

// persistTimeout bounds store writes made when a response finishes, which
// can happen after the request's own context is done.
const persistTimeout = 10 * time.Second

// responseTimings are the manager's polling and retry intervals.
type responseTimings struct {
	// cancelPoll is how often an in-flight response checks the store for a
	// cancel request from another instance.
	cancelPoll time.Duration
	// When saving a finished response fails, the save is retried with
	// backoff between retryMin and retryMax for retryWindow; meanwhile the
	// response stays readable in process.
	retryMin, retryMax, retryWindow time.Duration
}

var defaultResponseTimings = responseTimings{
	cancelPoll:  time.Second,
	retryMin:    time.Second,
	retryMax:    30 * time.Second,
	retryWindow: time.Hour,
}

// maxConversationTurns bounds how many linked responses are followed to
// rebuild a conversation.
const maxConversationTurns = 1000

// ResponseState is the in-process handle of a response. While a response
// registered with a ResponseManager is in flight, its state lives in this
// process (it holds the cancel function); finishing it (SetResult, SetError,
// Cancel) persists the outcome to the manager's store.
type ResponseState struct {
	sync.RWMutex
	ID         string
	Status     ResponseStatus
	Result     *ResponseObject
	Error      error
	model      string
	cancel     context.CancelFunc
	created_at time.Time

	manager  *ResponseManager // nil for states not registered with a manager
	owner    string
	conv     *emulatedConversation // conversation this response continues
	reply    *Message              // assistant reply to store as part of the turn
	done     chan struct{}         // closed when the response finishes
	finished bool
	deleted  bool       // deleted while in flight: never save it again
	saveMu   sync.Mutex // serialises store writes for this response with its deletion
}

// SetStatus updates the status of the response
func (r *ResponseState) SetStatus(status ResponseStatus) {
	r.Lock()
	defer r.Unlock()
	r.Status = status
}

// SetResult sets the result and marks the response as completed
func (r *ResponseState) SetResult(result *ResponseObject) {
	_ = r.finish(StatusCompleted, result, nil)
}

// SetError sets the error and marks the response as failed
func (r *ResponseState) SetError(err error) {
	_ = r.finish(StatusFailed, nil, err)
}

// GetStatus returns the current status
func (r *ResponseState) GetStatus() ResponseStatus {
	r.RLock()
	defer r.RUnlock()
	return r.Status
}

// GetResult returns the result (may be nil if not completed)
func (r *ResponseState) GetResult() *ResponseObject {
	r.RLock()
	defer r.RUnlock()
	return r.Result
}

// GetError returns the error (may be nil if not failed)
func (r *ResponseState) GetError() error {
	r.RLock()
	defer r.RUnlock()
	return r.Error
}

// Cancel cancels the response
func (r *ResponseState) Cancel() {
	r.RLock()
	cancel := r.cancel
	r.RUnlock()
	if cancel != nil {
		cancel()
	}
	_ = r.finish(StatusCancelled, nil, nil)
}

// finish records the outcome locally and, for a registered response,
// persists it and drops the in-process state. Only the first outcome counts:
// e.g. a stream failing because it was cancelled stays cancelled. If saving
// fails, the error is reported and the save retried in the background, with
// the response readable in this process meanwhile.
func (r *ResponseState) finish(status ResponseStatus, result *ResponseObject, err error) error {
	r.Lock()
	if r.finished {
		r.Unlock()
		return nil
	}
	r.finished = true
	r.Status = status
	r.Result = result
	r.Error = err
	m := r.manager
	if r.done != nil {
		close(r.done)
	}
	r.Unlock()
	if m == nil {
		return nil
	}
	if err := m.persist(r); err != nil {
		m.reportStoreError(r.ID, err)
		go m.retryPersist(r)
		return err
	}
	unregisterLive(r.ID)
	return nil
}

func (r *ResponseState) isDeleted() bool {
	r.RLock()
	defer r.RUnlock()
	return r.deleted
}

// Process-wide registry of in-flight responses, keyed by ID. IDs are random,
// so managers over different stores can share it.
var live = struct {
	sync.Mutex
	states map[string]*ResponseState
}{states: make(map[string]*ResponseState)}

func registerLive(s *ResponseState) {
	live.Lock()
	defer live.Unlock()
	live.states[s.ID] = s
}

func unregisterLive(id string) {
	live.Lock()
	defer live.Unlock()
	delete(live.states, id)
}

func lookupLive(id string) *ResponseState {
	live.Lock()
	defer live.Unlock()
	return live.states[id]
}

// ResponseManager tracks emulated responses: in-flight state in this
// process, everything else in a ResponseStore. A manager is scoped to an
// owner, and only sees responses created under that owner: clients scope
// their manager to their provider, base URL and API key, as native
// responses are scoped to the API key.
type ResponseManager struct {
	store                ResponseStore
	owner                string // hashed owner scope; "" when unscoped
	maxConversationBytes int    // 0 = DefaultMaxConversationBytes, <0 = unlimited
	onStoreError         func(responseID string, err error)
	timings              responseTimings
	backgroundTimeout    time.Duration // limit on an emulated background response's run (<= 0 = none)
}

// NewResponseManager creates a manager over a new in-memory store with
// default limits.
func NewResponseManager() *ResponseManager {
	return NewResponseManagerWithStore(NewMemoryResponseStore(MemoryResponseStoreOptions{}))
}

// NewResponseManagerWithStore creates a manager over the given store.
func NewResponseManagerWithStore(store ResponseStore) *ResponseManager {
	return &ResponseManager{store: store, timings: defaultResponseTimings, backgroundTimeout: DefaultRequestTimeout}
}

// NewClientResponseManager returns the manager a client uses: over
// config.ResponseStore (or the shared in-memory store), scoped to the
// provider, base URL and API key, so clients with different credentials
// can't see each other's responses.
func NewClientResponseManager(config Config, provider, baseURL string) *ResponseManager {
	base := GetManager()
	if config.ResponseStore != nil {
		base = NewResponseManagerWithStore(config.ResponseStore)
	}
	m := base.withOwner(provider + "\x00" + baseURL + "\x00" + config.APIKey)
	m.maxConversationBytes = config.MaxConversationBytes
	m.onStoreError = config.OnResponseStoreError
	if config.RequestTimeout != 0 {
		m.backgroundTimeout = config.RequestTimeout // negative: no limit, as for other requests
	}
	return m
}

// WithStoreErrorHandler returns a view of the manager that calls fn when
// saving a finished background or streamed response fails. Such saves are
// retried with backoff for up to an hour; fn is called for each failure,
// with a final error if the response is given up on.
func (m *ResponseManager) WithStoreErrorHandler(fn func(responseID string, err error)) *ResponseManager {
	scoped := *m
	scoped.onStoreError = fn
	return &scoped
}

func (m *ResponseManager) reportStoreError(id string, err error) {
	if m.onStoreError != nil {
		m.onStoreError(id, err)
	}
}

// retryPersist retries saving a finished response with backoff until it
// succeeds or persistRetryWindow passes, then drops the in-process state.
func (m *ResponseManager) retryPersist(s *ResponseState) {
	defer unregisterLive(s.ID)
	deadline := time.Now().Add(m.timings.retryWindow)
	backoff := m.timings.retryMin
	for {
		time.Sleep(backoff)
		if lookupLive(s.ID) != s || s.isDeleted() {
			return // deleted meanwhile
		}
		err := m.persist(s)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			m.reportStoreError(s.ID, fmt.Errorf("giving up saving response after %v: %w", m.timings.retryWindow, err))
			return
		}
		m.reportStoreError(s.ID, err)
		if backoff *= 2; backoff > m.timings.retryMax {
			backoff = m.timings.retryMax
		}
	}
}

// withOwner returns a view of the manager narrowed to owner: it sees only
// responses created through a view with the same owner chain.
func (m *ResponseManager) withOwner(owner string) *ResponseManager {
	if owner == "" {
		return m
	}
	sum := sha256.Sum256([]byte(m.owner + "\x00" + owner))
	scoped := *m
	scoped.owner = hex.EncodeToString(sum[:])
	return &scoped
}

// WithMaxConversationBytes returns a view of the manager that rejects
// continuing a conversation whose encoded size would exceed n bytes
// (0 = DefaultMaxConversationBytes, negative = unlimited).
func (m *ResponseManager) WithMaxConversationBytes(n int) *ResponseManager {
	scoped := *m
	scoped.maxConversationBytes = n
	return &scoped
}

// Store returns the manager's response store.
func (m *ResponseManager) Store() ResponseStore {
	return m.store
}

func (m *ResponseManager) conversationLimit() int {
	if m.maxConversationBytes == 0 {
		return DefaultMaxConversationBytes
	}
	return m.maxConversationBytes
}

// Create registers a new in-progress response with the given cancel function
// and model.
func (m *ResponseManager) Create(cancel context.CancelFunc, model string) *ResponseState {
	state, _ := m.begin(context.Background(), cancel, model, nil)
	return state
}

// begin registers a new in-progress response continuing conv (may be nil)
// and persists its in-progress record.
func (m *ResponseManager) begin(ctx context.Context, cancel context.CancelFunc, model string, conv *emulatedConversation) (*ResponseState, error) {
	state := &ResponseState{
		ID:         generateID(),
		Status:     StatusInProgress,
		model:      model,
		cancel:     cancel,
		created_at: time.Now(),
		manager:    m,
		owner:      m.owner,
		conv:       conv,
		done:       make(chan struct{}),
	}
	if err := m.store.Save(ctx, m.record(state)); err != nil {
		return nil, err
	}
	registerLive(state)
	if interval, ok := m.cancelPollInterval(); ok && cancel != nil {
		go m.watchForCancel(state, interval)
	}
	return state, nil
}

// cancelPoller is implemented by stores that choose how often in-flight
// responses check them for cancel requests from other instances: a negative
// interval disables checking (e.g. an in-process store, where no other
// instance can ask), zero leaves the default.
type cancelPoller interface {
	CancelPollInterval() time.Duration
}

// cancelPollInterval returns how often in-flight responses check the store
// for cancel requests, and false if they don't.
func (m *ResponseManager) cancelPollInterval() (time.Duration, bool) {
	if p, ok := m.store.(cancelPoller); ok {
		switch interval := p.CancelPollInterval(); {
		case interval < 0:
			return 0, false
		case interval > 0:
			return interval, true
		}
	}
	return m.timings.cancelPoll, true
}

// remoteStopWait is how long to wait for another instance to act on a
// cancel request: a few of its polls.
func (m *ResponseManager) remoteStopWait() time.Duration {
	interval, _ := m.cancelPollInterval()
	if wait := 5 * interval; wait > 2*time.Second {
		return wait
	}
	return 2 * time.Second
}

// watchForCancel cancels an in-flight response when another instance asks
// for it through the store.
func (m *ResponseManager) watchForCancel(s *ResponseState, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
			requested, err := m.store.CancelRequested(ctx, s.ID)
			cancel()
			if err == nil && requested {
				s.Cancel()
				return
			}
		}
	}
}

// record builds the stored record for a state.
func (m *ResponseManager) record(s *ResponseState) *StoredResponse {
	s.RLock()
	defer s.RUnlock()
	rec := &StoredResponse{
		ID:        s.ID,
		Owner:     s.owner,
		Model:     s.model,
		Status:    s.Status,
		Result:    s.Result,
		CreatedAt: s.created_at.Unix(),
	}
	if s.Error != nil {
		rec.Error = s.Error.Error()
	}
	if s.Status == StatusCompleted && s.conv != nil && s.reply != nil {
		rec.PreviousResponseID = s.conv.previousID
		rec.Ancestors = s.conv.ancestors
		rec.Turn = append(append([]Message{}, s.conv.input...), *s.reply)
		rec.ConversationBytes = s.conv.previousBytes + encodedSize(rec.Turn)
	}
	return rec
}

// persist saves a response's current state, unless it was deleted: saves
// and deletion are serialised, so a deleted response is never written back.
func (m *ResponseManager) persist(s *ResponseState) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.isDeleted() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
	defer cancel()
	return m.store.Save(ctx, m.record(s))
}

// saveCompleted stores a response that completed without ever being in
// flight (synchronous responses). Like a finished background response, if
// saving fails the error is reported and the save retried, with the
// response readable in this process meanwhile; the caller still gets it.
func (m *ResponseManager) saveCompleted(ctx context.Context, result *ResponseObject, model string, conv *emulatedConversation, reply Message) {
	state := &ResponseState{
		ID:         result.ID,
		Status:     StatusCompleted,
		Result:     result,
		model:      model,
		created_at: time.Now(),
		manager:    m,
		owner:      m.owner,
		conv:       conv,
		reply:      &reply,
		finished:   true,
	}
	if err := m.store.Save(ctx, m.record(state)); err != nil {
		m.reportStoreError(state.ID, err)
		registerLive(state)
		go m.retryPersist(state)
	}
}

// load returns the stored record for id if it belongs to this manager's owner.
func (m *ResponseManager) load(ctx context.Context, id string) (*StoredResponse, error) {
	rec, err := m.store.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.Owner != m.owner {
		return nil, ErrResponseNotFound
	}
	return rec, nil
}

// Get returns the state of a response: the in-process state while it's in
// flight here, otherwise a snapshot of its stored record.
func (m *ResponseManager) Get(id string) (*ResponseState, bool) {
	if s := lookupLive(id); s != nil {
		return s, s.owner == m.owner
	}
	rec, err := m.load(context.Background(), id)
	if err != nil {
		return nil, false
	}
	return stateFromRecord(rec), true
}

func stateFromRecord(rec *StoredResponse) *ResponseState {
	s := &ResponseState{
		ID:         rec.ID,
		Status:     rec.Status,
		Result:     rec.Result,
		model:      rec.Model,
		created_at: time.Unix(rec.CreatedAt, 0),
		owner:      rec.Owner,
	}
	if rec.Error != "" {
		s.Error = errors.New(rec.Error)
	}
	return s
}

// Cancel cancels a response by ID
func (m *ResponseManager) Cancel(id string) error {
	return m.cancelResponse(context.Background(), id)
}

// cancelResponse cancels a response. One running on another instance is
// asked to stop through the store, and waited for briefly.
func (m *ResponseManager) cancelResponse(ctx context.Context, id string) error {
	if s := lookupLive(id); s != nil {
		if s.owner != m.owner {
			return fmt.Errorf("%w: %s", ErrResponseNotFound, id)
		}
		s.Cancel()
		return nil
	}
	rec, err := m.load(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrResponseNotFound, id)
	}
	if isInProgress(rec.Status) {
		return m.stopRemote(ctx, id)
	}
	rec.Status = StatusCancelled
	rec.Result = nil
	rec.Turn = nil
	return m.store.Save(ctx, rec)
}

// Delete removes a response, cancelling it first if it's in flight here.
// Deleting a response also ends the conversations continuing from it, since
// its turn is no longer available to rebuild them.
func (m *ResponseManager) Delete(id string) {
	_ = m.deleteResponse(context.Background(), id)
}

func (m *ResponseManager) deleteResponse(ctx context.Context, id string) error {
	if s := lookupLive(id); s != nil {
		if s.owner != m.owner {
			return fmt.Errorf("%w: %s", ErrResponseNotFound, id)
		}
		s.Lock()
		s.deleted = true
		cancel, running := s.cancel, isInProgress(s.Status)
		s.Unlock()
		if running && cancel != nil {
			cancel()
		}
		// Wait for any save already under way, so it can't land after the delete
		s.saveMu.Lock()
		defer s.saveMu.Unlock()
		unregisterLive(id)
		return m.store.Delete(ctx, id)
	}
	rec, err := m.load(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrResponseNotFound, id)
	}
	if isInProgress(rec.Status) {
		// Running on another instance: have it stop first, so it can't
		// write the response back after it's deleted
		if err := m.stopRemote(ctx, id); err != nil {
			return err
		}
	}
	return m.store.Delete(ctx, id)
}

// stopRemote asks the instance running a response to cancel it, and waits
// for it to stop.
func (m *ResponseManager) stopRemote(ctx context.Context, id string) error {
	if _, ok := m.cancelPollInterval(); !ok {
		return fmt.Errorf("response %s is in progress elsewhere, and this store doesn't support cancelling it from here", id)
	}
	if err := m.store.RequestCancel(ctx, id); err != nil {
		return err
	}
	if err := m.waitUntilFinished(ctx, id, m.remoteStopWait()); err != nil {
		return fmt.Errorf("cancellation of response %s requested, but it is still in progress on another instance: it stops when that instance next checks, or expires if the instance has gone: %w", id, err)
	}
	return nil
}

// waitUntilFinished waits up to wait for a response to stop being in progress.
func (m *ResponseManager) waitUntilFinished(ctx context.Context, id string, wait time.Duration) error {
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		rec, err := m.store.Load(ctx, id)
		if errors.Is(err, ErrResponseNotFound) || (err == nil && !isInProgress(rec.Status)) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			return fmt.Errorf("timed out after %v", wait)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// emulatedConversation is the conversation a request continues: the stored
// history of its previous response, if any, plus the request's new input.
// Instructions are not part of it; as on the native API they apply to one
// request only.
type emulatedConversation struct {
	previousID    string
	ancestors     []string  // the new response's ancestors: previousID's ancestors plus previousID
	previousBytes int       // stored size of the conversation up to previousID
	history       []Message // turns of the previous responses, oldest first
	input         []Message // the request's new input
}

func (c *emulatedConversation) messages() []Message {
	return append(append([]Message{}, c.history...), c.input...)
}

// conversation rebuilds the conversation for a request continuing
// previousID (may be empty) with input, enforcing ownership and the size
// limit.
func (m *ResponseManager) conversation(ctx context.Context, previousID string, input []any) (*emulatedConversation, error) {
	conv := &emulatedConversation{previousID: previousID, input: ConvertInputToMessages(input)}
	if previousID != "" {
		prev, err := m.load(ctx, previousID)
		if errors.Is(err, ErrResponseNotFound) {
			return nil, fmt.Errorf("previous %w: %s", ErrResponseNotFound, previousID)
		}
		if err != nil {
			return nil, err
		}
		if prev.Status != StatusCompleted {
			return nil, fmt.Errorf("previous response %s is %s, not completed", previousID, prev.Status)
		}
		if len(prev.Ancestors)+1 >= maxConversationTurns {
			return nil, fmt.Errorf("conversation is longer than %d turns; compact it", maxConversationTurns)
		}

		// One batched read for the rest of the conversation
		earlier, err := m.store.LoadMany(ctx, prev.Ancestors)
		if err != nil {
			return nil, err
		}
		for i, rec := range earlier {
			if rec == nil || rec.Owner != m.owner || rec.Status != StatusCompleted {
				return nil, fmt.Errorf("conversation history for %s is no longer available: an earlier response (%s) expired or was deleted", previousID, prev.Ancestors[i])
			}
			conv.history = append(conv.history, rec.Turn...)
		}
		conv.history = append(conv.history, prev.Turn...)
		conv.previousBytes = prev.ConversationBytes
		conv.ancestors = append(append([]string{}, prev.Ancestors...), previousID)
		if err := m.store.Touch(ctx, conv.ancestors...); err != nil {
			return nil, err
		}
	}
	if limit := m.conversationLimit(); limit > 0 {
		if size := conv.previousBytes + encodedSize(conv.input); size > limit {
			return nil, fmt.Errorf("conversation is %d bytes, over the limit of %d; compact it", size, limit)
		}
	}
	return conv, nil
}

func encodedSize(msgs []Message) int {
	data, _ := json.Marshal(msgs)
	return len(data)
}

// generateID generates a unique response ID using UUIDv7
// UUIDv7 is time-ordered and collision-resistant
func generateID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// Fallback to timestamp-based ID if UUID generation fails
		return fmt.Sprintf("resp_%d_%d", time.Now().UnixMilli(), time.Now().UnixNano())
	}
	return "resp_" + id.String()
}

// Global response manager (singleton)
var (
	globalManager     *ResponseManager
	globalManagerOnce sync.Once
	globalManagerLock sync.RWMutex
)

// GetManager returns the global response manager, over a shared in-memory
// store with default limits. It is unscoped; clients use scoped views of it
// (see NewClientResponseManager).
func GetManager() *ResponseManager {
	globalManagerLock.RLock()
	defer globalManagerLock.RUnlock()
	globalManagerOnce.Do(func() {
		globalManager = NewResponseManager()
	})
	return globalManager
}

// Shutdown clears the global manager (useful for tests)
func Shutdown() {
	globalManagerLock.Lock()
	defer globalManagerLock.Unlock()
	globalManager = nil
	globalManagerOnce = sync.Once{}
}
