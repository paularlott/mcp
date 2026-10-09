package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type backgroundDoneKey struct{}

type backgroundDone struct {
	ctx  context.Context
	resp *ResponseObject
	err  error
}

func recordBackgroundDone() (func(context.Context, *ResponseObject, error), <-chan backgroundDone) {
	ch := make(chan backgroundDone, 4)
	return func(ctx context.Context, resp *ResponseObject, err error) {
		ch <- backgroundDone{ctx, resp, err}
	}, ch
}

func waitBackgroundDone(t *testing.T, ch <-chan backgroundDone) backgroundDone {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("OnBackgroundResponseDone not called")
		return backgroundDone{}
	}
}

// An emulated background response reports its result and usage to the hook,
// with the creating request's context values.
func TestOnBackgroundResponseDone_Emulated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
		})
	}))
	defer srv.Close()
	hook, done := recordBackgroundDone()
	c, err := New(Config{BaseURL: srv.URL, OnBackgroundResponseDone: hook})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), backgroundDoneKey{}, "tenant-a")
	r, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	d := waitBackgroundDone(t, done)
	if d.err != nil || d.resp == nil || d.resp.ID != r.ID || d.resp.OutputText() != "hi" {
		t.Fatalf("hook got %+v, %v", d.resp, d.err)
	}
	if d.resp.Usage == nil || d.resp.Usage.InputTokens != 7 || d.resp.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v", d.resp.Usage)
	}
	if d.ctx.Value(backgroundDoneKey{}) != "tenant-a" {
		t.Error("hook context lost the request's values")
	}

	// Synchronous responses are the caller's to account for
	if _, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", Input: userInput("y")}); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-done:
		t.Errorf("hook called for a synchronous response: %+v", d.resp)
	case <-time.After(100 * time.Millisecond):
	}
}

// A failed emulated background response reports the error.
func TestOnBackgroundResponseDone_EmulatedFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad model","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()
	hook, done := recordBackgroundDone()
	c, _ := New(Config{BaseURL: srv.URL, OnBackgroundResponseDone: hook, MaxRetries: -1})

	if _, err := c.CreateResponse(context.Background(), CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")}); err != nil {
		t.Fatal(err)
	}
	if d := waitBackgroundDone(t, done); d.err == nil || d.resp != nil {
		t.Errorf("hook got %+v, %v; want an error", d.resp, d.err)
	}
}

// A native background response run locally (xAI can't run it) is reported too.
func TestOnBackgroundResponseDone_NativeRunLocally(t *testing.T) {
	f := &fakeNative{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	hook, done := recordBackgroundDone()
	native := true
	c, _ := New(Config{Provider: providerGrok, BaseURL: srv.URL, UseNativeResponses: &native, OnBackgroundResponseDone: hook})

	ctx := context.WithValue(context.Background(), backgroundDoneKey{}, "tenant-b")
	if _, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "grok", Background: true, Input: userInput("x")}); err != nil {
		t.Fatal(err)
	}
	d := waitBackgroundDone(t, done)
	if d.err != nil || d.resp == nil || d.resp.OutputText() != "ok" {
		t.Fatalf("hook got %+v, %v", d.resp, d.err)
	}
	if d.ctx.Value(backgroundDoneKey{}) != "tenant-b" {
		t.Error("hook context lost the request's values")
	}
}
