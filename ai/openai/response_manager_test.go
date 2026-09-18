package openai

import (
	"errors"
	"testing"
	"time"
)

func TestResponseState_SetStatus(t *testing.T) {
	s := &ResponseState{Status: StatusQueued}
	s.SetStatus(StatusInProgress)
	if s.GetStatus() != StatusInProgress {
		t.Errorf("GetStatus() = %v, want %v", s.GetStatus(), StatusInProgress)
	}
}

func TestResponseState_SetResult(t *testing.T) {
	s := &ResponseState{Status: StatusInProgress}
	resp := &ResponseObject{ID: "resp_1"}
	s.SetResult(resp)
	if s.GetStatus() != StatusCompleted {
		t.Errorf("GetStatus() = %v, want %v", s.GetStatus(), StatusCompleted)
	}
	if got := s.GetResult(); got != resp {
		t.Errorf("GetResult() = %v, want %v", got, resp)
	}
}

func TestResponseState_SetError(t *testing.T) {
	s := &ResponseState{Status: StatusInProgress}
	err := errors.New("boom")
	s.SetError(err)
	if s.GetStatus() != StatusFailed {
		t.Errorf("GetStatus() = %v, want %v", s.GetStatus(), StatusFailed)
	}
	if got := s.GetError(); got != err {
		t.Errorf("GetError() = %v, want %v", got, err)
	}
}

func TestResponseState_GetResult_Nil(t *testing.T) {
	s := &ResponseState{}
	if got := s.GetResult(); got != nil {
		t.Errorf("GetResult() = %v, want nil", got)
	}
}

func TestResponseState_GetError_Nil(t *testing.T) {
	s := &ResponseState{}
	if got := s.GetError(); got != nil {
		t.Errorf("GetError() = %v, want nil", got)
	}
}

func TestResponseState_Cancel(t *testing.T) {
	cancelled := false
	s := &ResponseState{
		Status: StatusInProgress,
		cancel: func() { cancelled = true },
	}
	s.Cancel()
	if !cancelled {
		t.Error("expected cancel func to be called")
	}
	if s.GetStatus() != StatusCancelled {
		t.Errorf("GetStatus() = %v, want %v", s.GetStatus(), StatusCancelled)
	}
}

func TestResponseState_Cancel_NilCancelFunc(t *testing.T) {
	s := &ResponseState{Status: StatusInProgress}
	// Should not panic with nil cancel func.
	s.Cancel()
	if s.GetStatus() != StatusCancelled {
		t.Errorf("GetStatus() = %v, want %v", s.GetStatus(), StatusCancelled)
	}
}

func TestResponseManager_CreateAndGet(t *testing.T) {
	m := NewResponseManager()
	state := m.Create(func() {}, "gpt-x")
	if state.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	got, ok := m.Get(state.ID)
	if !ok || got != state {
		t.Errorf("Get() = %v, %v, want %v, true", got, ok, state)
	}
}

func TestResponseManager_Get_NotFound(t *testing.T) {
	m := NewResponseManager()
	_, ok := m.Get("does-not-exist")
	if ok {
		t.Error("expected ok=false for missing ID")
	}
}

func TestResponseManager_Cancel(t *testing.T) {
	m := NewResponseManager()
	cancelled := false
	state := m.Create(func() { cancelled = true }, "gpt-x")
	if err := m.Cancel(state.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cancelled {
		t.Error("expected cancel func called")
	}
	if state.GetStatus() != StatusCancelled {
		t.Errorf("GetStatus() = %v, want cancelled", state.GetStatus())
	}
}

func TestResponseManager_Cancel_NotFound(t *testing.T) {
	m := NewResponseManager()
	if err := m.Cancel("nope"); err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestResponseManager_Delete(t *testing.T) {
	m := NewResponseManager()
	state := m.Create(func() {}, "gpt-x")
	m.Delete(state.ID)
	if _, ok := m.Get(state.ID); ok {
		t.Error("expected response to be deleted")
	}
}

func TestResponseManager_Delete_NonExistent(t *testing.T) {
	m := NewResponseManager()
	// Deleting a non-existent ID should not panic.
	m.Delete("does-not-exist")
}

func TestResponseManager_CleanupOldResponses(t *testing.T) {
	m := NewResponseManager()

	// A completed response, artificially aged.
	oldState := m.Create(func() {}, "gpt-x")
	oldState.SetResult(&ResponseObject{ID: oldState.ID})
	oldState.Lock()
	oldState.created_at = time.Now().Add(-1 * time.Hour)
	oldState.Unlock()

	// A recent completed response.
	newState := m.Create(func() {}, "gpt-x")
	newState.SetResult(&ResponseObject{ID: newState.ID})

	// An in-progress response, also artificially aged — must NOT be cleaned up.
	inProgress := m.Create(func() {}, "gpt-x")
	inProgress.Lock()
	inProgress.created_at = time.Now().Add(-1 * time.Hour)
	inProgress.Unlock()

	m.CleanupOldResponses(10 * time.Minute)

	if _, ok := m.Get(oldState.ID); ok {
		t.Error("expected old completed response to be cleaned up")
	}
	if _, ok := m.Get(newState.ID); !ok {
		t.Error("expected recent response to remain")
	}
	if _, ok := m.Get(inProgress.ID); !ok {
		t.Error("expected in-progress response to remain regardless of age")
	}
}

func TestResponseManager_StartCleanupTask(t *testing.T) {
	m := NewResponseManager()
	oldState := m.Create(func() {}, "gpt-x")
	oldState.SetResult(&ResponseObject{ID: oldState.ID})
	oldState.Lock()
	oldState.created_at = time.Now().Add(-1 * time.Hour)
	oldState.Unlock()

	m.StartCleanupTask(10*time.Millisecond, 1*time.Millisecond)

	deadline := time.After(2 * time.Second)
	for {
		if _, ok := m.Get(oldState.ID); !ok {
			break // cleaned up
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for cleanup task to remove old response")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Calling StartCleanupTask again must be a no-op (sync.Once) and not panic.
	m.StartCleanupTask(10*time.Millisecond, 1*time.Millisecond)
}

func TestGetManager_And_Shutdown(t *testing.T) {
	Shutdown() // ensure clean slate regardless of prior test state
	m1 := GetManager()
	m2 := GetManager()
	if m1 != m2 {
		t.Error("expected GetManager() to return the same singleton instance")
	}

	Shutdown()
	m3 := GetManager()
	if m3 == m1 {
		t.Error("expected Shutdown() to reset the singleton, got same instance")
	}
}

func TestGenerateID_UniqueAndPrefixed(t *testing.T) {
	id1 := generateID()
	id2 := generateID()
	if id1 == id2 {
		t.Errorf("expected unique IDs, got %q twice", id1)
	}
	if !hasPrefix(id1, "resp_") {
		t.Errorf("id1 = %q, want resp_ prefix", id1)
	}
}
