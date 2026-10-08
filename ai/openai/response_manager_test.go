package openai

import (
	"errors"
	"testing"
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

func TestResponseManager_FinishedResponsesLeaveProcessState(t *testing.T) {
	m := NewResponseManager()
	state := m.Create(func() {}, "gpt-x")
	if lookupLive(state.ID) == nil {
		t.Fatal("in-progress response should be held in process")
	}
	state.SetResult(&ResponseObject{ID: state.ID})
	if lookupLive(state.ID) != nil {
		t.Error("finished response should no longer be held in process")
	}
	got, ok := m.Get(state.ID)
	if !ok || got.GetStatus() != StatusCompleted || got.GetResult().ID != state.ID {
		t.Errorf("Get() = %+v, %v; want completed snapshot from the store", got, ok)
	}
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
