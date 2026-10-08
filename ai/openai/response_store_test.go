package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- ownership ---

func TestOwnership_ClientScopesAreIsolated(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	alice := NewClientResponseManager(Config{APIKey: "key-a", ResponseStore: store}, "openai", "https://api.example.com/v1/")
	bob := NewClientResponseManager(Config{APIKey: "key-b", ResponseStore: store}, "openai", "https://api.example.com/v1/")
	otherHost := NewClientResponseManager(Config{APIKey: "key-a", ResponseStore: store}, "openai", "https://other.example.com/v1/")
	ctx := context.Background()
	mc := &mockCompleter{resp: replyWith("secret reply")}

	r, err := CreateResponseEmulated(ctx, mc, alice, CreateResponseRequest{Model: "m", Input: userInput("my secret")})
	if err != nil {
		t.Fatal(err)
	}
	assertInvisible(t, ctx, mc, store, r.ID, bob, otherHost, GetManager())

	// The owner still has full access
	if _, err := GetResponseEmulated(ctx, alice, r.ID); err != nil {
		t.Fatalf("owner GetResponse: %v", err)
	}
	if _, err := CreateResponseEmulated(ctx, mc, alice, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("again")}); err != nil {
		t.Fatalf("owner continue: %v", err)
	}
}

func TestOwnership_OwnerCanUseEveryOperation(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	client := NewClientResponseManager(Config{APIKey: "key", ResponseStore: store}, "openai", "")
	other := NewClientResponseManager(Config{APIKey: "other-key", ResponseStore: store}, "openai", "")
	mc := &mockCompleter{resp: replyWith("secret reply")}
	ctx := context.Background()

	r, err := CreateResponseEmulated(ctx, mc, client, CreateResponseRequest{Model: "m", Input: userInput("my secret")})
	if err != nil {
		t.Fatal(err)
	}
	assertInvisibleCtx(t, ctx, mc, store, r.ID, other)

	if _, err := GetResponseEmulated(ctx, client, r.ID); err != nil {
		t.Errorf("owner GetResponse: %v", err)
	}
	if _, err := CreateResponseEmulated(ctx, mc, client, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("again")}); err != nil {
		t.Errorf("owner continue: %v", err)
	}
	if _, err := CompactResponseEmulated(ctx, mc, client, CompactResponseRequest{Model: "m", PreviousResponseID: r.ID}); err != nil {
		t.Errorf("owner compact: %v", err)
	}
	bc := newBlockingCompleter()
	running, err := CreateResponseEmulated(ctx, bc, client, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := CancelResponseEmulated(ctx, client, running.ID); err != nil || got.Status != "cancelled" {
		t.Errorf("owner cancel = %+v, %v", got, err)
	}
	if err := DeleteResponseEmulated(ctx, client, r.ID); err != nil {
		t.Errorf("owner delete: %v", err)
	}
}

// assertInvisible checks that none of the managers can see, continue,
// compact, cancel or delete response id, and that the record survives.
func assertInvisible(t *testing.T, ctx context.Context, mc *mockCompleter, store ResponseStore, id string, managers ...*ResponseManager) {
	t.Helper()
	for i, m := range managers {
		t.Run(fmt.Sprint("manager", i), func(t *testing.T) {
			assertInvisibleCtx(t, ctx, mc, store, id, m)
		})
	}
}

func assertInvisibleCtx(t *testing.T, ctx context.Context, mc *mockCompleter, store ResponseStore, id string, m *ResponseManager) {
	t.Helper()
	calls := mc.calls
	if _, err := GetResponseEmulated(ctx, m, id); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("GetResponse error = %v, want not found", err)
	}
	if _, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: id, Input: userInput("what did I say?")}); err == nil {
		t.Error("continue succeeded across owners")
	}
	if _, err := CompactResponseEmulated(ctx, mc, m, CompactResponseRequest{Model: "m", PreviousResponseID: id}); err == nil {
		t.Error("compact succeeded across owners")
	}
	if _, err := CancelResponseEmulated(ctx, m, id); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("CancelResponse error = %v, want not found", err)
	}
	if err := DeleteResponseEmulated(ctx, m, id); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("DeleteResponse error = %v, want not found", err)
	}
	if _, ok := m.Get(id); ok {
		t.Error("Get found the response across owners")
	}
	if mc.calls != calls {
		t.Error("model was called with another owner's conversation")
	}
	if rec, err := store.Load(context.Background(), id); err != nil || rec.Status != StatusCompleted {
		t.Errorf("record after cross-owner attempts = %+v, %v; want intact", rec, err)
	}
}

func TestOwnership_InFlightResponseCantBeCancelledByOthers(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	alice := NewClientResponseManager(Config{APIKey: "a", ResponseStore: store}, "openai", "")
	bob := NewClientResponseManager(Config{APIKey: "b", ResponseStore: store}, "openai", "")
	ctx := context.Background()
	bc := newBlockingCompleter()

	r, err := CreateResponseEmulated(ctx, bc, alice, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CancelResponseEmulated(ctx, bob, r.ID); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("cross-owner cancel error = %v, want not found", err)
	}
	if err := DeleteResponseEmulated(ctx, bob, r.ID); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("cross-owner delete error = %v, want not found", err)
	}
	if state, _ := alice.Get(r.ID); state.GetStatus() != StatusInProgress {
		t.Errorf("status = %s, want still in progress", state.GetStatus())
	}
	got, err := CancelResponseEmulated(ctx, alice, r.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("owner cancel = %+v, %v", got, err)
	}
}

// Real clients with different API keys share the process-wide store but
// can't see each other's responses.
func TestOwnership_ClientsWithDifferentKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{ID: "c", Choices: []Choice{{
			Message: Message{Role: "assistant", Content: fmt.Sprintf("saw %d messages", len(req.Messages))}, FinishReason: "stop",
		}}})
	}))
	defer srv.Close()

	a, _ := New(Config{BaseURL: srv.URL, APIKey: "key-a"})
	b, _ := New(Config{BaseURL: srv.URL, APIKey: "key-b"})
	ctx := context.Background()
	r, err := a.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: userInput("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetResponse(ctx, r.ID); err == nil {
		t.Error("client with another key read the response")
	}
	if _, err := b.CreateResponse(ctx, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("x")}); err == nil {
		t.Error("client with another key continued the conversation")
	}
	if err := b.DeleteResponse(ctx, r.ID); err == nil {
		t.Error("client with another key deleted the response")
	}
	next, err := a.CreateResponse(ctx, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("x")})
	if err != nil || next.OutputText() != "saw 3 messages" {
		t.Fatalf("owner continue = %q, %v", next.OutputText(), err)
	}
}

// --- limits and storage layout ---

func TestLimits_MaxConversationBytes(t *testing.T) {
	m := NewResponseManager().WithMaxConversationBytes(300)
	mc := &mockCompleter{resp: replyWith(strings.Repeat("x", 100))}
	ctx := context.Background()

	r1, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", Input: userInput("hello")})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: r1.ID, Input: userInput("again")})
	if err != nil {
		t.Fatal(err)
	}
	calls := mc.calls
	_, err = CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: r2.ID, Input: userInput("and again")})
	if err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("error = %v, want conversation size limit", err)
	}
	if mc.calls != calls {
		t.Error("model called for an oversized conversation")
	}
	if _, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", Input: userInput(strings.Repeat("y", 400))}); err == nil {
		t.Error("oversized new input accepted")
	}
	if _, err := CreateResponseEmulated(ctx, mc, m.WithMaxConversationBytes(-1), CreateResponseRequest{Model: "m", PreviousResponseID: r2.ID, Input: userInput("x")}); err != nil {
		t.Errorf("unlimited manager: %v", err)
	}
}

func TestStorage_EachTurnStoredOnce(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	m := NewResponseManagerWithStore(store)
	mc := &mockCompleter{resp: replyWith("ok")}
	ctx := context.Background()

	prev := ""
	var ids []string
	for i := 0; i < 5; i++ {
		r, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: prev, Input: userInput(fmt.Sprint("turn ", i))})
		if err != nil {
			t.Fatal(err)
		}
		prev = r.ID
		ids = append(ids, r.ID)
	}
	if len(mc.lastReq.Messages) != 9 {
		t.Fatalf("last request had %d messages, want 9", len(mc.lastReq.Messages))
	}
	for i, id := range ids {
		rec, err := store.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.Turn) != 2 || rec.Turn[0].GetContentAsString() != fmt.Sprint("turn ", i) {
			t.Errorf("record %d turn = %+v, want only its own input and reply", i, rec.Turn)
		}
	}
}

func TestStorage_DeletingAnEarlierTurnEndsTheConversation(t *testing.T) {
	m := NewResponseManager()
	mc := &mockCompleter{resp: replyWith("ok")}
	ctx := context.Background()
	r1, _ := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", Input: userInput("secret")})
	r2, _ := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: r1.ID, Input: userInput("more")})
	if err := DeleteResponseEmulated(ctx, m, r1.ID); err != nil {
		t.Fatal(err)
	}
	_, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: r2.ID, Input: userInput("x")})
	if err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("error = %v, want history no longer available", err)
	}
}

func TestMemoryStore_EvictsLeastRecentlyUsed(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{MaxResponses: 2})
	ctx := context.Background()
	save := func(id string, status ResponseStatus) {
		if err := store.Save(ctx, &StoredResponse{ID: id, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	save("a", StatusCompleted)
	save("b", StatusCompleted)
	store.Touch(ctx, "a") // b is now least recently used
	save("c", StatusCompleted)
	if _, err := store.Load(ctx, "b"); !errors.Is(err, ErrResponseNotFound) {
		t.Error("least recently used record not evicted")
	}
	for _, id := range []string{"a", "c"} {
		if _, err := store.Load(ctx, id); err != nil {
			t.Errorf("%s evicted: %v", id, err)
		}
	}

	// In-progress records are never evicted
	running := NewMemoryResponseStore(MemoryResponseStoreOptions{MaxResponses: 1})
	running.Save(ctx, &StoredResponse{ID: "r1", Status: StatusInProgress})
	running.Save(ctx, &StoredResponse{ID: "r2", Status: StatusInProgress})
	if running.Len() != 2 {
		t.Errorf("in-progress records evicted: len = %d", running.Len())
	}
}

func TestMemoryStore_MaxBytes(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{MaxBytes: 1000})
	ctx := context.Background()
	big := []Message{{Role: "user", Content: strings.Repeat("x", 400)}}
	for _, id := range []string{"a", "b", "c"} {
		if err := store.Save(ctx, &StoredResponse{ID: id, Status: StatusCompleted, Turn: big}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Load(ctx, "a"); !errors.Is(err, ErrResponseNotFound) {
		t.Error("oldest record not evicted when over the byte limit")
	}
	if err := store.Save(ctx, &StoredResponse{ID: "huge", Turn: []Message{{Role: "user", Content: strings.Repeat("x", 2000)}}}); err == nil {
		t.Error("record larger than the store accepted")
	}
}

func TestMemoryStore_IdleExpiry(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{TTL: time.Minute})
	now := time.Now()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	store.Save(ctx, &StoredResponse{ID: "idle", Status: StatusCompleted})
	store.Save(ctx, &StoredResponse{ID: "used", Status: StatusCompleted})
	store.Save(ctx, &StoredResponse{ID: "running", Status: StatusInProgress})

	now = now.Add(45 * time.Second)
	store.Touch(ctx, "used")
	now = now.Add(45 * time.Second)

	if _, err := store.Load(ctx, "idle"); !errors.Is(err, ErrResponseNotFound) {
		t.Error("idle record did not expire")
	}
	if _, err := store.Load(ctx, "used"); err != nil {
		t.Errorf("recently used record expired: %v", err)
	}
	if _, err := store.Load(ctx, "running"); err != nil {
		t.Errorf("in-progress record expired: %v", err)
	}
}

func TestMemoryStore_RecordsAreCopies(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	ctx := context.Background()
	rec := &StoredResponse{ID: "a", Status: StatusCompleted, Result: &ResponseObject{ID: "a", Model: "m"}}
	store.Save(ctx, rec)
	rec.Result.Model = "changed"
	got, _ := store.Load(ctx, "a")
	got.Result.Model = "changed again"
	again, _ := store.Load(ctx, "a")
	if again.Result.Model != "m" {
		t.Errorf("stored record mutated: %q", again.Result.Model)
	}
}

// --- store: false ---

func TestStoreFalse_NothingKept(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	m := NewResponseManagerWithStore(store)
	mc := &mockCompleter{resp: replyWith("ok")}
	ctx := context.Background()
	noStore := false

	r, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", Store: &noStore, Input: userInput("private")})
	if err != nil || r.ID == "" || r.OutputText() != "ok" {
		t.Fatalf("response = %+v, %v", r, err)
	}
	if _, err := GetResponseEmulated(ctx, m, r.ID); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("GetResponse error = %v, want not found", err)
	}

	streamEmulated(t, &recordingStreamCompleter{reply: "ok"}, m, CreateResponseRequest{Model: "m", Store: &noStore, Input: userInput("private")})

	if store.Len() != 0 {
		t.Errorf("store holds %d records, want 0", store.Len())
	}
	if _, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", Store: &noStore, Background: true, Input: userInput("x")}); err == nil {
		t.Error("background response with store: false accepted")
	}
}

// --- key-value store (e.g. Redis) shared between instances ---

type fakeKV struct {
	mu     sync.Mutex
	data   map[string][]byte
	ttls   map[string]time.Duration
	failed bool
	calls  map[string]int // round trips by method
}

func newFakeKV() *fakeKV {
	return &fakeKV{data: map[string][]byte{}, ttls: map[string]time.Duration{}, calls: map[string]int{}}
}

func (f *fakeKV) setFailed(failed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = failed
}

func (f *fakeKV) roundTrips() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for k, v := range f.calls {
		out[k] = v
	}
	return out
}

func (f *fakeKV) ttl(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ttls[key]
}

func (f *fakeKV) Get(ctx context.Context, keys ...string) ([][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["Get"]++
	if f.failed {
		return nil, errors.New("kv unavailable")
	}
	out := make([][]byte, len(keys))
	for i, k := range keys {
		out[i] = f.data[k]
	}
	return out, nil
}

func (f *fakeKV) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["Set"]++
	if f.failed {
		return errors.New("kv unavailable")
	}
	f.data[key] = value
	f.ttls[key] = ttl
	return nil
}

func (f *fakeKV) Delete(ctx context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["Delete"]++
	if f.failed {
		return errors.New("kv unavailable")
	}
	for _, k := range keys {
		delete(f.data, k)
		delete(f.ttls, k)
	}
	return nil
}

func (f *fakeKV) Expire(ctx context.Context, ttl time.Duration, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["Expire"]++
	if f.failed {
		return errors.New("kv unavailable")
	}
	for _, k := range keys {
		if _, ok := f.data[k]; ok {
			f.ttls[k] = ttl
		}
	}
	return nil
}

// fast shortens a manager's cancel polling and save retries for tests.
func fast(m *ResponseManager) *ResponseManager {
	scoped := *m
	scoped.timings = responseTimings{cancelPoll: 10 * time.Millisecond, retryMin: 10 * time.Millisecond, retryMax: 20 * time.Millisecond, retryWindow: time.Minute}
	return &scoped
}

func TestKVStore_SharedBetweenInstances(t *testing.T) {
	kv := newFakeKV()
	newInstance := func() *ResponseManager {
		store := NewKVResponseStore(kv, KVResponseStoreOptions{TTL: time.Hour})
		return NewClientResponseManager(Config{APIKey: "k", ResponseStore: store}, "openai", "")
	}
	instanceA, instanceB := newInstance(), newInstance()
	ctx := context.Background()

	mc := &mockCompleter{resp: &ChatCompletionResponse{Choices: []Choice{{Message: Message{
		Role:      "assistant",
		ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "f", Arguments: map[string]any{"n": 1.0}}}},
	}}}}}
	r1, err := CreateResponseEmulated(ctx, mc, instanceA, CreateResponseRequest{
		Model: "m", Input: userInput("call f"), Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "f"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ttl := kv.ttl("mcp:response:" + r1.ID); ttl != time.Hour {
		t.Errorf("ttl = %v, want 1h", ttl)
	}

	// Instance B continues the conversation started on A
	mc.resp = replyWith("done")
	if _, err := CreateResponseEmulated(ctx, mc, instanceB, CreateResponseRequest{
		Model: "m", PreviousResponseID: r1.ID,
		Input: []any{map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "42"}},
	}); err != nil {
		t.Fatal(err)
	}
	msgs := mc.lastReq.Messages
	if len(msgs) != 3 || msgs[1].ToolCalls[0].Function.Arguments["n"] != 1.0 || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("messages rebuilt on instance B = %+v", msgs)
	}

	// Another key on the same KV store sees nothing
	other := NewClientResponseManager(Config{APIKey: "other", ResponseStore: NewKVResponseStore(kv, KVResponseStoreOptions{})}, "openai", "")
	if _, err := GetResponseEmulated(ctx, other, r1.ID); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("other key GetResponse error = %v", err)
	}

	if kv.ttl("mcp:response:"+r1.ID) != time.Hour {
		t.Errorf("ttl after touch = %v", kv.ttl("mcp:response:"+r1.ID))
	}
}

// newKVInstances returns two managers over one key-value store, as two
// instances of a service would have.
func newKVInstances(kv *fakeKV) (*ResponseManager, *ResponseManager) {
	newInstance := func() *ResponseManager {
		return fast(NewClientResponseManager(Config{APIKey: "k", ResponseStore: NewKVResponseStore(kv, KVResponseStoreOptions{})}, "openai", ""))
	}
	return newInstance(), newInstance()
}

// startElsewhere starts a background response on instance a and hides its
// in-process state, as instance b would run in another process.
func startElsewhere(t *testing.T, a *ResponseManager, completer ChatCompleter) string {
	t.Helper()
	r, err := CreateResponseEmulated(context.Background(), completer, a, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	state := lookupLive(r.ID)
	unregisterLive(r.ID)
	t.Cleanup(func() {
		if state != nil {
			state.Cancel()
		}
	})
	return r.ID
}

func TestKVStore_CancelAcrossInstances(t *testing.T) {
	kv := newFakeKV()
	instanceA, instanceB := newKVInstances(kv)
	bc := newBlockingCompleter()
	ctx := context.Background()
	r, err := CreateResponseEmulated(ctx, bc, instanceA, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	id := r.ID
	unregisterLive(id) // instance B can't see A's in-process state

	if state, ok := instanceB.Get(id); !ok || state.GetStatus() != StatusInProgress {
		t.Fatalf("instance B view = %+v, %v", state, ok)
	}
	otherKey := fast(NewClientResponseManager(Config{APIKey: "other", ResponseStore: NewKVResponseStore(kv, KVResponseStoreOptions{})}, "openai", ""))
	if _, err := CancelResponseEmulated(ctx, otherKey, id); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("cancel with another key error = %v", err)
	}
	got, err := CancelResponseEmulated(ctx, instanceB, id)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("cancel from B = %+v, %v", got, err)
	}
	if rec, _ := kv.Get(context.Background(), "mcp:response:"+id); rec[0] == nil {
		t.Error("cancelled record missing")
	}
}

func TestKVStore_StreamCancelledAcrossInstances(t *testing.T) {
	kv := newFakeKV()
	instanceA, instanceB := newKVInstances(kv)

	// A stream on A that never ends on its own
	started := make(chan string, 1)
	sc := &endlessStreamCompleter{}
	errorChan := make(chan error, 1)
	eventChan := make(chan ResponseStreamEvent, 100)
	go func() {
		StreamResponseEmulatedWithManager(context.Background(), sc, instanceA, CreateResponseRequest{Model: "m", Input: userInput("x")}, eventChan, errorChan)
		close(errorChan)
	}()
	go func() {
		for ev := range eventChan {
			if ev.Type == "response.created" {
				var v struct {
					Response ResponseObject `json:"response"`
				}
				json.Unmarshal(ev.Data, &v)
				started <- v.Response.ID
			}
		}
	}()
	id := <-started
	unregisterLive(id) // instance B can't see A's in-process state

	got, err := CancelResponseEmulated(context.Background(), instanceB, id)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("cancel from B = %+v, %v", got, err)
	}
	select {
	case <-errorChan:
	case <-time.After(5 * time.Second):
		t.Fatal("stream on A did not stop")
	}
	close(eventChan)
}

// endlessStreamCompleter streams one chunk then blocks until cancelled.
type endlessStreamCompleter struct{ mockCompleter }

func (e *endlessStreamCompleter) StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream {
	respCh := make(chan ChatCompletionResponse, 1)
	errCh := make(chan error, 1)
	respCh <- ChatCompletionResponse{Choices: []Choice{{Delta: Delta{Content: "..."}}}}
	go func() {
		<-ctx.Done()
		errCh <- ctx.Err()
		close(errCh)
		close(respCh)
	}()
	return NewChatStream(ctx, respCh, errCh)
}

func TestKVStore_DeleteAcrossInstances(t *testing.T) {
	kv := newFakeKV()
	instanceA, instanceB := newKVInstances(kv)
	bc := newBlockingCompleter()
	id := startElsewhere(t, instanceA, bc)

	if err := DeleteResponseEmulated(context.Background(), instanceB, id); err != nil {
		t.Fatalf("delete from B: %v", err)
	}
	// A was stopped before the delete, so nothing writes the record back
	time.Sleep(100 * time.Millisecond)
	if _, err := GetResponseEmulated(context.Background(), instanceA, id); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("GetResponse after delete error = %v, want not found", err)
	}
}

func TestKVStore_ContinuingIsBatched(t *testing.T) {
	kv := newFakeKV()
	m := NewResponseManagerWithStore(NewKVResponseStore(kv, KVResponseStoreOptions{}))
	mc := &mockCompleter{resp: replyWith("ok")}
	ctx := context.Background()
	prev := ""
	for i := 0; i < 25; i++ {
		r, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: prev, Input: userInput(fmt.Sprint("turn ", i))})
		if err != nil {
			t.Fatal(err)
		}
		prev = r.ID
	}

	before := kv.roundTrips()
	if _, err := CreateResponseEmulated(ctx, mc, m, CreateResponseRequest{Model: "m", PreviousResponseID: prev, Input: userInput("last")}); err != nil {
		t.Fatal(err)
	}
	after := kv.roundTrips()
	if len(mc.lastReq.Messages) != 51 {
		t.Fatalf("messages = %d, want 51", len(mc.lastReq.Messages))
	}
	for method, want := range map[string]int{"Get": 2, "Expire": 1, "Set": 1} {
		if got := after[method] - before[method]; got != want {
			t.Errorf("%s round trips = %d, want %d for a 25-turn conversation", method, got, want)
		}
	}
}

func TestKVStore_FailedBackgroundSaveIsReportedAndRetried(t *testing.T) {
	kv := newFakeKV()
	var mu sync.Mutex
	var reported []error
	instanceA, instanceB := newKVInstances(kv)
	instanceA = instanceA.WithStoreErrorHandler(func(id string, err error) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	})

	bc := newBlockingCompleter()
	r, err := CreateResponseEmulated(context.Background(), bc, instanceA, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	<-bc.started
	kv.setFailed(true)
	close(bc.release)

	// The failure is reported, and the result stays readable on A meanwhile
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(reported)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("store failure not reported")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if got, err := GetResponseEmulated(context.Background(), instanceA, r.ID); err != nil || got.OutputText() != "ok" {
		t.Fatalf("instance A GetResponse while the store is down = %+v, %v", got, err)
	}

	// Once the store recovers the retry saves it, and B sees the result
	kv.setFailed(false)
	got, err := GetResponseEmulated(context.Background(), instanceB, r.ID)
	if err != nil || got.OutputText() != "ok" {
		t.Fatalf("instance B GetResponse after recovery = %+v, %v", got, err)
	}
	for lookupLive(r.ID) != nil {
		select {
		case <-deadline:
			t.Fatal("in-process state not dropped after the retry succeeded")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// A store failure never fails a response: it is returned (or streamed to
// completion), the failure reported, and the save retried.
func TestStoreFailure_ResponsesStillDelivered(t *testing.T) {
	kv := newFakeKV()
	var mu sync.Mutex
	var reported []string
	m := fast(NewResponseManagerWithStore(NewKVResponseStore(kv, KVResponseStoreOptions{}))).WithStoreErrorHandler(func(id string, err error) {
		mu.Lock()
		reported = append(reported, id)
		mu.Unlock()
	})
	ctx := context.Background()

	kv.setFailed(true)
	r, err := CreateResponseEmulated(ctx, &mockCompleter{resp: replyWith("paid for")}, m, CreateResponseRequest{Model: "m", Input: userInput("x")})
	if err != nil || r.OutputText() != "paid for" {
		t.Fatalf("sync response with the store down = %+v, %v; want the response", r, err)
	}
	if got, err := GetResponseEmulated(ctx, m, r.ID); err != nil || got.OutputText() != "paid for" {
		t.Errorf("readable in process while the store is down = %+v, %v", got, err)
	}

	// A stream whose final save fails still completes
	kv.setFailed(false)
	gate := &failingAfterStart{kv: kv}
	streamed := streamEmulated(t, gate, m, CreateResponseRequest{Model: "m", Input: userInput("y")})
	if streamed.Status != "completed" {
		t.Errorf("stream status = %q", streamed.Status)
	}

	mu.Lock()
	n := len(reported)
	mu.Unlock()
	if n < 2 {
		t.Errorf("store failures reported = %d, want both responses", n)
	}

	// The retries save both once the store recovers
	kv.setFailed(false)
	for _, id := range []string{r.ID, streamed.ID} {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if rec, err := m.store.Load(ctx, id); err == nil && rec.Status == StatusCompleted {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("response %s not saved after the store recovered", id)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// failingAfterStart streams a reply, taking the key-value store down before
// the response's final save.
type failingAfterStart struct {
	streamMockCompleter
	kv *fakeKV
}

func (f *failingAfterStart) StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream {
	f.kv.setFailed(true)
	f.chunks = []ChatCompletionResponse{{Choices: []Choice{{Delta: Delta{Content: "ok"}}}}, {Choices: []Choice{{FinishReason: "stop"}}}}
	return f.streamMockCompleter.StreamChatCompletion(ctx, req)
}

// gatedStore holds saves of finished responses until released.
type gatedStore struct {
	*MemoryResponseStore
	entered chan struct{}
	release chan struct{}
}

func (g *gatedStore) Save(ctx context.Context, rec *StoredResponse) error {
	if !isInProgress(rec.Status) {
		close(g.entered)
		<-g.release
	}
	return g.MemoryResponseStore.Save(ctx, rec)
}

// Deleting a response whose final save is under way waits for the save,
// then deletes: the response is never written back.
func TestDelete_SaveUnderWayCantResurrect(t *testing.T) {
	store := &gatedStore{MemoryResponseStore: NewMemoryResponseStore(MemoryResponseStoreOptions{}), entered: make(chan struct{}), release: make(chan struct{})}
	m := NewResponseManagerWithStore(store)
	bc := newBlockingCompleter()
	r, err := CreateResponseEmulated(context.Background(), bc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	close(bc.release)
	<-store.entered // the completed save is under way

	deleted := make(chan error, 1)
	go func() { deleted <- DeleteResponseEmulated(context.Background(), m, r.ID) }()
	select {
	case <-deleted:
		t.Fatal("delete returned while a save was under way")
	case <-time.After(50 * time.Millisecond):
	}
	close(store.release)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), r.ID); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("response resurrected after delete: %v", err)
	}
}

// Deleting an in-flight response: the run unwinding afterwards doesn't
// write it back.
func TestDelete_InFlightRunCantResurrect(t *testing.T) {
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{})
	m := NewResponseManagerWithStore(store)
	bc := newBlockingCompleter()
	r, err := CreateResponseEmulated(context.Background(), bc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	<-bc.started
	state := lookupLive(r.ID)
	if err := DeleteResponseEmulated(context.Background(), m, r.ID); err != nil {
		t.Fatal(err)
	}
	<-state.done // the run has unwound and tried to record its outcome
	if _, err := store.Load(context.Background(), r.ID); !errors.Is(err, ErrResponseNotFound) {
		t.Errorf("response resurrected after delete: %v", err)
	}
}

func TestBackgroundTimeoutFollowsRequestTimeout(t *testing.T) {
	m := NewClientResponseManager(Config{RequestTimeout: 50 * time.Millisecond, ResponseStore: NewMemoryResponseStore(MemoryResponseStoreOptions{})}, "openai", "")
	bc := newBlockingCompleter() // never released
	r, err := CreateResponseEmulated(context.Background(), bc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = GetResponseEmulated(context.Background(), m, r.ID)
	// The stored error is text, so match its message
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("error = %v, want the request timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("background response ran %v, past its 50ms request timeout", elapsed)
	}
}

func TestCancelOnDeadInstance(t *testing.T) {
	kv := newFakeKV()
	m, _ := newKVInstances(kv)
	ctx := context.Background()
	// An in-progress record whose instance is gone: nothing will act on it
	state, err := m.begin(ctx, nil, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	unregisterLive(state.ID)

	start := time.Now()
	_, err = CancelResponseEmulated(ctx, m, state.ID)
	if err == nil || !strings.Contains(err.Error(), "still in progress on another instance") {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancel took %v", elapsed)
	}
	if err := DeleteResponseEmulated(ctx, m, state.ID); err == nil {
		t.Error("delete of a response stuck in progress elsewhere succeeded")
	}
}

func TestCancelPolling(t *testing.T) {
	if _, ok := NewResponseManager().cancelPollInterval(); ok {
		t.Error("in-process store should not poll for cancel requests")
	}
	kv := newFakeKV()
	polled := NewResponseManagerWithStore(NewKVResponseStore(kv, KVResponseStoreOptions{CancelPollInterval: 3 * time.Second}))
	if interval, ok := polled.cancelPollInterval(); !ok || interval != 3*time.Second {
		t.Errorf("interval = %v, %v", interval, ok)
	}

	disabled := NewResponseManagerWithStore(NewKVResponseStore(kv, KVResponseStoreOptions{CancelPollInterval: -1}))
	state, err := disabled.begin(context.Background(), func() {}, "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	unregisterLive(state.ID)
	if _, err := CancelResponseEmulated(context.Background(), disabled, state.ID); err == nil || !strings.Contains(err.Error(), "doesn't support") {
		t.Errorf("cancel with polling disabled error = %v", err)
	}
	before := kv.roundTrips()["Get"]
	time.Sleep(50 * time.Millisecond)
	if kv.roundTrips()["Get"] != before {
		t.Error("in-flight response polled the store with polling disabled")
	}
}
