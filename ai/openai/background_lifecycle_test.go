package openai

import (
	"context"
	"testing"
	"time"
)

// Cancelling a running background response always leaves it cancelled, even
// when the run notices its context ending before the cancel is recorded.
func TestCancelBackground_AlwaysCancelled(t *testing.T) {
	m := NewResponseManager()
	ctx := context.Background()
	for i := 0; i < 300; i++ {
		bc := newBlockingCompleter()
		r, err := CreateResponseEmulated(ctx, bc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
		if err != nil {
			t.Fatal(err)
		}
		<-bc.started
		got, err := CancelResponseEmulated(ctx, m, r.ID)
		if err != nil || got.Status != "cancelled" {
			t.Fatalf("run %d: cancel = %+v, %v; want cancelled", i, got, err)
		}
	}
}

// A finished background response releases its context at once, rather than
// holding it (and the request's values) until the timeout.
func TestBackground_ReleasesContextWhenDone(t *testing.T) {
	m := NewResponseManager()
	cc := &ctxCapturingCompleter{}
	r, err := CreateResponseEmulated(context.Background(), cc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetResponseEmulated(context.Background(), m, r.ID); err != nil {
		t.Fatal(err)
	}
	cc.mu.Lock()
	ctx := cc.ctx
	cc.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("background context still live after the response finished")
	}
}

// slowStore delays saves, widening the gap between a response finishing and
// its outcome being stored.
type slowStore struct {
	*MemoryResponseStore
}

func (s slowStore) Save(ctx context.Context, rec *StoredResponse) error {
	if !isInProgress(rec.Status) {
		time.Sleep(50 * time.Millisecond)
	}
	return s.MemoryResponseStore.Save(ctx, rec)
}

// Once GetResponse reports a background response completed, it can be
// continued straight away: completion is only reported once it is stored.
func TestBackground_ContinueAsSoonAsCompleted(t *testing.T) {
	m := NewResponseManagerWithStore(slowStore{NewMemoryResponseStore(MemoryResponseStoreOptions{})})
	mc := &mockCompleter{resp: &ChatCompletionResponse{ID: "c", Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}}}
	for i := 0; i < 5; i++ {
		r, err := CreateResponseEmulated(context.Background(), mc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
		if err != nil {
			t.Fatal(err)
		}
		got, err := GetResponseEmulated(context.Background(), m, r.ID)
		if err != nil || got.Status != "completed" {
			t.Fatalf("get = %+v, %v", got, err)
		}
		if _, err := CreateResponseEmulated(context.Background(), mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("y")}); err != nil {
			t.Fatalf("continue straight after completion: %v", err)
		}
	}
}
