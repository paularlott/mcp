package openai

import (
	"context"
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

// ResponseState holds the state of an async response
type ResponseState struct {
	sync.RWMutex
	ID         string
	Status     ResponseStatus
	Result     *ResponseObject
	Error      error
	cancel     context.CancelFunc
	created_at time.Time
}

// SetStatus updates the status of the response
func (r *ResponseState) SetStatus(status ResponseStatus) {
	r.Lock()
	defer r.Unlock()
	r.Status = status
}

// SetResult sets the result and marks the response as completed
func (r *ResponseState) SetResult(result *ResponseObject) {
	r.Lock()
	defer r.Unlock()
	r.Result = result
	r.Status = StatusCompleted
}

// SetError sets the error and marks the response as failed
func (r *ResponseState) SetError(err error) {
	r.Lock()
	defer r.Unlock()
	r.Error = err
	r.Status = StatusFailed
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
	r.Lock()
	defer r.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	r.Status = StatusCancelled
}

// ResponseManager manages in-memory async responses
type ResponseManager struct {
	sync.RWMutex
	responses        map[string]*ResponseState
	startCleanupOnce sync.Once
}

// NewResponseManager creates a new response manager
func NewResponseManager() *ResponseManager {
	return &ResponseManager{
		responses: make(map[string]*ResponseState),
	}
}

// Create creates a new response state with the given cancel function
func (m *ResponseManager) Create(cancel context.CancelFunc) *ResponseState {
	m.Lock()
	defer m.Unlock()

	id := generateID()
	state := &ResponseState{
		ID:         id,
		Status:     StatusInProgress,
		cancel:     cancel,
		created_at: time.Now(),
	}
	m.responses[id] = state
	return state
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

// Get retrieves a response state by ID
func (m *ResponseManager) Get(id string) (*ResponseState, bool) {
	m.RLock()
	defer m.RUnlock()
	state, ok := m.responses[id]
	return state, ok
}

// Cancel cancels a response by ID
func (m *ResponseManager) Cancel(id string) error {
	state, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("response not found")
	}
	state.Cancel()
	return nil
}

// Delete removes a response from the manager
func (m *ResponseManager) Delete(id string) {
	m.Lock()
	defer m.Unlock()
	delete(m.responses, id)
}

// CleanupOldResponses removes responses older than the given duration
func (m *ResponseManager) CleanupOldResponses(maxAge time.Duration) {
	m.Lock()
	defer m.Unlock()

	now := time.Now()
	for id, state := range m.responses {
		state.RLock()
		age := now.Sub(state.created_at)
		// Only clean up completed/failed/cancelled responses
		if state.Status != StatusInProgress && state.Status != StatusQueued && age > maxAge {
			state.RUnlock()
			delete(m.responses, id)
		} else {
			state.RUnlock()
		}
	}
}

// StartCleanupTask starts a background task to periodically clean up old responses
// Only starts the cleanup task once (uses sync.Once internally)
func (m *ResponseManager) StartCleanupTask(interval time.Duration, maxAge time.Duration) {
	m.startCleanupOnce.Do(func() {
		ticker := time.NewTicker(interval)
		go func() {
			for range ticker.C {
				m.CleanupOldResponses(maxAge)
			}
		}()
	})
}

// Global response manager (singleton)
var (
	globalManager      *ResponseManager
	globalManagerOnce  sync.Once
	globalManagerLock  sync.RWMutex
)

// GetManager returns the global response manager, initializing it on first use
func GetManager() *ResponseManager {
	globalManagerOnce.Do(func() {
		globalManager = NewResponseManager()
		// Start cleanup task (1 min interval, 15 min max age)
		globalManager.StartCleanupTask(1*time.Minute, 15*time.Minute)
	})
	return globalManager
}

// Shutdown stops the cleanup task and clears the manager (useful for tests)
func Shutdown() {
	globalManagerLock.Lock()
	defer globalManagerLock.Unlock()
	globalManager = nil
	globalManagerOnce = sync.Once{}
}
