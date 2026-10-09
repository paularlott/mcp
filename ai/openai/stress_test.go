package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStress_ResponsesUnderLoad runs a mix of Responses API operations from
// many goroutines against a fake provider: synchronous, continued, streamed,
// abandoned streams, background (polled, cancelled, deleted) and chat
// streams dropped mid-way. It checks that nothing fails unexpectedly, that
// the store stays within its limits, that no response is left in flight and
// that goroutines don't leak. Run with MCP_STRESS=1 (MCP_STRESS_OPS sets the
// number of operations, default 5000).
func TestStress_ResponsesUnderLoad(t *testing.T) {
	if os.Getenv("MCP_STRESS") == "" {
		t.Skip("set MCP_STRESS=1 to run the stress test")
	}
	ops := 5000
	if n := os.Getenv("MCP_STRESS_OPS"); n != "" {
		fmt.Sscan(n, &ops)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case <-time.After(time.Duration(rand.Intn(15)) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		text := strings.Repeat("word ", 20+rand.Intn(400))
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for i, part := range strings.SplitAfter(text, " ")[:10] {
				fmt.Fprintf(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", part)
				flusher.Flush()
				if i%3 == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":10,\"total_tokens\":13}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":10,"total_tokens":13}}`, text)
	}))
	defer srv.Close()

	const maxResponses, maxBytes = 300, 1 << 20
	store := NewMemoryResponseStore(MemoryResponseStoreOptions{MaxResponses: maxResponses, MaxBytes: maxBytes})
	client, err := New(Config{BaseURL: srv.URL, ResponseStore: store, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	var (
		recentMu sync.Mutex
		recent   []string
		counts   sync.Map // op -> *int64
		failures []string
		failMu   sync.Mutex
	)
	count := func(op string) {
		v, _ := counts.LoadOrStore(op, new(int64))
		atomic.AddInt64(v.(*int64), 1)
	}
	fail := func(op string, err error) {
		failMu.Lock()
		defer failMu.Unlock()
		if len(failures) < 20 {
			failures = append(failures, fmt.Sprintf("%s: %v", op, err))
		}
	}
	remember := func(id string) {
		recentMu.Lock()
		defer recentMu.Unlock()
		recent = append(recent, id)
		if len(recent) > 50 {
			recent = recent[len(recent)-50:]
		}
	}
	pick := func() string {
		recentMu.Lock()
		defer recentMu.Unlock()
		if len(recent) == 0 {
			return ""
		}
		return recent[rand.Intn(len(recent))]
	}
	// A continued or deleted response may have been evicted or deleted by
	// another worker meanwhile
	gone := func(err error) bool {
		return errors.Is(err, ErrResponseNotFound) || strings.Contains(err.Error(), "can't be continued") ||
			strings.Contains(err.Error(), "no longer available")
	}
	input := []any{map[string]any{"role": "user", "content": "hello"}}

	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 64; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range work {
				ctx := context.Background()
				switch op := rand.Intn(8); op {
				case 0: // synchronous
					r, err := client.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: input})
					if err != nil {
						fail("sync", err)
						continue
					}
					remember(r.ID)
					count("sync")
				case 1: // continue a recent response
					prev := pick()
					r, err := client.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: input, PreviousResponseID: prev})
					if err != nil {
						if !gone(err) {
							fail("continue", err)
						}
						continue
					}
					remember(r.ID)
					count("continue")
				case 2: // full stream
					st := client.StreamResponse(ctx, CreateResponseRequest{Model: "m", Input: input})
					var id string
					for st.Next() {
						if e := st.Current(); e.Type == "response.completed" {
							r := e.Response()
							if r != nil {
								id = r.ID
							}
						}
					}
					if err := st.Err(); err != nil || id == "" {
						fail("stream", fmt.Errorf("id=%q err=%v", id, err))
						continue
					}
					remember(id)
					count("stream")
				case 3: // stream abandoned after the first event
					sctx, cancel := context.WithCancel(ctx)
					st := client.StreamResponse(sctx, CreateResponseRequest{Model: "m", Input: input})
					st.Next()
					cancel()
					for st.Next() {
					}
					count("stream-abandoned")
				case 4: // background, polled to completion
					r, err := client.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: input, Background: true})
					if err != nil {
						fail("background", err)
						continue
					}
					got, err := client.GetResponse(ctx, r.ID)
					if err != nil || got.Status != "completed" {
						fail("background-get", fmt.Errorf("%v %v", got, err))
						continue
					}
					remember(r.ID)
					count("background")
				case 5: // background, cancelled at once
					r, err := client.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: input, Background: true})
					if err != nil {
						fail("background-cancel", err)
						continue
					}
					got, err := client.CancelResponse(ctx, r.ID)
					if err != nil || (got.Status != "cancelled" && got.Status != "completed") {
						fail("background-cancel", fmt.Errorf("%v %v", got, err))
						continue
					}
					count("background-cancel")
				case 6: // background deleted while running, or a recent one deleted
					id := pick()
					if rand.Intn(2) == 0 {
						r, err := client.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: input, Background: true})
						if err != nil {
							fail("delete", err)
							continue
						}
						id = r.ID
					}
					if id == "" {
						continue
					}
					if err := client.DeleteResponse(ctx, id); err != nil && !gone(err) {
						fail("delete", err)
						continue
					}
					count("delete")
				case 7: // chat stream dropped mid-way
					sctx, cancel := context.WithCancel(ctx)
					st := client.StreamChatCompletion(sctx, ChatCompletionRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
					st.Next()
					cancel()
					for st.Next() {
					}
					count("chat-dropped")
				}
			}
		}()
	}
	start := time.Now()
	for i := 0; i < ops; i++ {
		work <- i
	}
	close(work)
	wg.Wait()
	elapsed := time.Since(start)

	var summary []string
	counts.Range(func(k, v any) bool {
		summary = append(summary, fmt.Sprintf("%s=%d", k, atomic.LoadInt64(v.(*int64))))
		return true
	})
	t.Logf("%d operations in %v (%.0f/s): %s", ops, elapsed, float64(ops)/elapsed.Seconds(), strings.Join(summary, " "))
	for _, f := range failures {
		t.Error(f)
	}

	// Store limits hold
	if n := store.Len(); n > maxResponses {
		t.Errorf("store holds %d responses, limit %d", n, maxResponses)
	}
	store.mu.Lock()
	bytes, cancels := store.bytes, len(store.cancels)
	store.mu.Unlock()
	if bytes > maxBytes {
		t.Errorf("store holds %d bytes, limit %d", bytes, maxBytes)
	}

	// Nothing left in flight, nothing leaked
	deadline := time.Now().Add(10 * time.Second)
	for {
		live.Lock()
		inFlight := len(live.states)
		live.Unlock()
		srv.CloseClientConnections()
		runtime.GC()
		goroutines := runtime.NumGoroutine()
		if inFlight == 0 && goroutines <= baseGoroutines+5 {
			t.Logf("store: %d responses, %d bytes, %d cancel marks; goroutines %d (baseline %d)", store.Len(), bytes, cancels, goroutines, baseGoroutines)
			break
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("after load: %d responses in flight, %d goroutines (baseline %d)\n%s", inFlight, goroutines, baseGoroutines, buf[:n])
		}
		time.Sleep(100 * time.Millisecond)
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	t.Logf("heap after GC: %.1f MiB allocated, %.1f MiB in use", float64(mem.HeapAlloc)/(1<<20), float64(mem.HeapInuse)/(1<<20))
}
