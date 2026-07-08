package openai

import (
	"context"
	"errors"
	"testing"
)

// helper to drain a stream and return (chunks, err)
func drainStream(s *ChatStream) ([]ChatCompletionResponse, error) {
	var chunks []ChatCompletionResponse
	for s.Next() {
		chunks = append(chunks, s.Current())
	}
	return chunks, s.Err()
}

// --- Error before any data ---

func TestChatStream_ErrorBeforeData(t *testing.T) {
	testErr := errors.New("upstream failure")
	respCh := make(chan ChatCompletionResponse)
	errCh := make(chan error, 1)
	errCh <- testErr
	// Don't close respCh — forces Next() to read from errCh
	s := NewChatStream(context.Background(), respCh, errCh)

	chunks, err := drainStream(s)
	if err != testErr {
		t.Fatalf("Err() = %v, want %v", err, testErr)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
	if !s.Done() {
		t.Fatal("Done() = false, want true")
	}
}

// --- Normal completion: data then clean close ---

func TestChatStream_NormalCompletion(t *testing.T) {
	respCh := make(chan ChatCompletionResponse, 2)
	errCh := make(chan error, 1)
	respCh <- ChatCompletionResponse{ID: "chunk1", Model: "m1"}
	respCh <- ChatCompletionResponse{ID: "chunk2", Model: "m1"}
	close(respCh)
	close(errCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	chunks, err := drainStream(s)
	if err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ID != "chunk1" || chunks[1].ID != "chunk2" {
		t.Fatalf("chunk order wrong: %s, %s", chunks[0].ID, chunks[1].ID)
	}
}

// --- THE RACE: both channels ready simultaneously with error ---
// This is the bug that was fixed. When responseChan is closed AND errorChan
// has a buffered error, Go's select is non-deterministic. The select may
// deliver the buffered chunk first OR read the error first — either way,
// the error MUST be captured by the time the stream ends.

func TestChatStream_RaceBothClosedWithError(t *testing.T) {
	testErr := errors.New("mid-stream failure")
	respCh := make(chan ChatCompletionResponse, 1)
	errCh := make(chan error, 1)
	respCh <- ChatCompletionResponse{ID: "chunk1", Model: "m1"}
	close(respCh)
	errCh <- testErr
	close(errCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	chunks, err := drainStream(s)
	// The error MUST always be captured — this is what the fix guarantees.
	if err != testErr {
		t.Fatalf("Err() = %v, want %v (error was lost — race bug)", err, testErr)
	}
	// Chunk count is 0 or 1 depending on which channel select picks first.
	// What matters is the error is never lost.
	if len(chunks) > 1 {
		t.Fatalf("expected at most 1 chunk, got %d", len(chunks))
	}
}

// Run the race test many times to exercise both select paths
func TestChatStream_RaceBothClosedWithError_Repeated(t *testing.T) {
	testErr := errors.New("failure")
	for i := 0; i < 200; i++ {
		respCh := make(chan ChatCompletionResponse, 1)
		errCh := make(chan error, 1)
		respCh <- ChatCompletionResponse{ID: "c", Model: "m"}
		close(respCh)
		errCh <- testErr
		close(errCh)
		s := NewChatStream(context.Background(), respCh, errCh)

		_, err := drainStream(s)
		if err != testErr {
			t.Fatalf("iteration %d: Err() = %v, want %v", i, err, testErr)
		}
	}
}

// --- Both channels closed simultaneously, NO error ---
func TestChatStream_BothClosedNoError(t *testing.T) {
	respCh := make(chan ChatCompletionResponse)
	errCh := make(chan error)
	close(respCh)
	close(errCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	chunks, err := drainStream(s)
	if err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

// --- Empty stream: no error, no data ---
func TestChatStream_EmptyStream(t *testing.T) {
	respCh := make(chan ChatCompletionResponse)
	errCh := make(chan error, 1)
	close(respCh)
	close(errCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	chunks, err := drainStream(s)
	if err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

// --- Error after some data (truly sequential: error sent AFTER chunk consumed) ---
func TestChatStream_ErrorAfterData(t *testing.T) {
	testErr := errors.New("late failure")
	respCh := make(chan ChatCompletionResponse, 1)
	errCh := make(chan error, 1)

	// Put a chunk but NO error yet — no race on first Next().
	respCh <- ChatCompletionResponse{ID: "chunk1", Model: "m1"}
	s := NewChatStream(context.Background(), respCh, errCh)

	if !s.Next() {
		t.Fatal("expected first chunk")
	}
	if s.Current().ID != "chunk1" {
		t.Fatalf("expected chunk1, got %s", s.Current().ID)
	}

	// Now send the error — second Next() picks it up.
	errCh <- testErr

	if s.Next() {
		t.Fatal("expected stream to end with error")
	}
	if s.Err() != testErr {
		t.Fatalf("Err() = %v, want %v", s.Err(), testErr)
	}
}

// --- Context cancellation ---
func TestChatStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	respCh := make(chan ChatCompletionResponse) // blocks
	errCh := make(chan error)                    // blocks
	s := NewChatStream(ctx, respCh, errCh)

	go func() {
		cancel()
	}()

	_, err := drainStream(s)
	if err != context.Canceled {
		t.Fatalf("Err() = %v, want %v", err, context.Canceled)
	}
}

// --- errorChan closed first (no error), then data drains ---
func TestChatStream_ErrorChanClosedThenDataDrains(t *testing.T) {
	respCh := make(chan ChatCompletionResponse, 2)
	errCh := make(chan error, 1)
	respCh <- ChatCompletionResponse{ID: "chunk1"}
	respCh <- ChatCompletionResponse{ID: "chunk2"}
	close(errCh) // no error — Next() should nil it out and continue draining
	close(respCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	chunks, err := drainStream(s)
	if err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

// --- Current() returns zero value when stream not started ---
func TestChatStream_CurrentBeforeNext(t *testing.T) {
	respCh := make(chan ChatCompletionResponse)
	errCh := make(chan error)
	close(respCh)
	close(errCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	// Current() before Next() returns zero value
	c := s.Current()
	if c.ID != "" {
		t.Fatalf("expected zero-value Current(), got ID=%s", c.ID)
	}
}

// --- Done() is false until Next() returns false ---
func TestChatStream_DoneLifecycle(t *testing.T) {
	respCh := make(chan ChatCompletionResponse, 1)
	errCh := make(chan error, 1)
	respCh <- ChatCompletionResponse{ID: "chunk1"}
	close(respCh)
	close(errCh)
	s := NewChatStream(context.Background(), respCh, errCh)

	if s.Done() {
		t.Fatal("Done() should be false before consuming stream")
	}

	s.Next()
	if s.Done() {
		t.Fatal("Done() should be false after first chunk")
	}

	s.Next()
	if !s.Done() {
		t.Fatal("Done() should be true after stream ends")
	}

	// Calling Next() again on a done stream should return false immediately
	if s.Next() {
		t.Fatal("Next() on done stream should return false")
	}
}
