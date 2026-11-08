package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// handlerServer has one tool that asks a question with defaults and reports
// the answer, or why it couldn't ask.
func handlerServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("handler-test", "1")
	sm, err := NewJWTSessionManagerWithAutoKey(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSessionManager(sm)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":   map[string]any{"type": "string"},
			"colour": map[string]any{"type": "string", "enum": []any{"red", "blue"}, "default": "blue"},
			"count":  map[string]any{"type": "integer", "default": 3},
		},
		"required": []any{"name"},
	}
	s.RegisterTool(NewTool("ask", "asks"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		res, err := Elicit(ctx, ElicitForm{Key: "q", Message: "Who are you?", Schema: schema})
		switch {
		case errors.Is(err, ErrElicitationUnsupported):
			return NewToolResponseText("unsupported"), nil
		case err != nil:
			return nil, err
		case res.Accepted():
			return NewToolResponseText(fmt.Sprintf("accept name=%v colour=%v count=%v", res.Content["name"], res.Content["colour"], res.Content["count"])), nil
		}
		return NewToolResponseText(string(res.Action)), nil
	})
	return s
}

// OnElicitation answers a server's questions in both eras, over HTTP and
// stdio: an accepted answer gets the schema's defaults for the fields left
// out, decline and cancel pass through, and a failing handler is a cancel.
func TestClientOnElicitation(t *testing.T) {
	s := handlerServer(t)
	for _, transport := range []string{"http", "stdio"} {
		for _, legacy := range []bool{true, false} {
			era := map[bool]string{true: "legacy", false: "modern"}[legacy]
			t.Run(transport+"-"+era, func(t *testing.T) {
				var mu sync.Mutex
				var asked []ElicitationRequest
				var answer func() (*ElicitResult, error)
				// The handler is registered before Initialize, which is when a
				// Legacy client declares elicitation.
				c := connectClient(t, s, transport, legacy, func(c *Client) {
					c.OnElicitation(func(ctx context.Context, req ElicitationRequest) (*ElicitResult, error) {
						mu.Lock()
						asked = append(asked, req)
						mu.Unlock()
						return answer()
					})
				})
				// The handler opens the Legacy GET stream; close it before the
				// test server shuts down (cleanups run last-in first-out).
				t.Cleanup(func() { c.Close() })
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				callText := func() string {
					t.Helper()
					resp, err := c.CallTool(ctx, "ask", nil)
					if err != nil {
						t.Fatalf("CallTool: %v", err)
					}
					return responseText(resp)
				}

				answer = func() (*ElicitResult, error) {
					return &ElicitResult{Action: ElicitAccept, Content: map[string]any{"name": "Ada"}}, nil
				}
				if got := callText(); got != "accept name=Ada colour=blue count=3" {
					t.Errorf("accept: got %q", got)
				}
				mu.Lock()
				if len(asked) != 1 || asked[0].Message != "Who are you?" || asked[0].Schema["properties"] == nil {
					t.Errorf("handler saw %+v", asked)
				}
				mu.Unlock()

				answer = func() (*ElicitResult, error) { return &ElicitResult{Action: ElicitDecline}, nil }
				if got := callText(); got != "decline" {
					t.Errorf("decline: got %q", got)
				}
				answer = func() (*ElicitResult, error) { return nil, errors.New("no user here") }
				if got := callText(); got != "cancel" {
					t.Errorf("handler error: got %q, want cancel", got)
				}
			})
		}
	}
}

// Without a handler, and not forwarding for a Server, the Client doesn't
// declare elicitation in either era, so the server knows it can't ask
// (rather than being told every time that the user cancelled).
func TestClientWithoutElicitationHandler(t *testing.T) {
	s := handlerServer(t)
	for _, legacy := range []bool{true, false} {
		c := connectClient(t, s, "http", legacy, nil)
		t.Cleanup(func() { c.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		resp, err := c.CallTool(ctx, "ask", nil)
		cancel()
		if err != nil {
			t.Fatalf("legacy=%v: %v", legacy, err)
		}
		if got := responseText(resp); got != "unsupported" {
			t.Errorf("legacy=%v: got %q, want unsupported", legacy, got)
		}
	}
}

func TestWithSchemaDefaults(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{
		"a": map[string]any{"type": "string", "default": "x"},
		"b": map[string]any{"type": "boolean", "default": false},
		"c": map[string]any{"type": "number"},
	}}
	in := map[string]any{"a": "given"}
	got := withSchemaDefaults(schema, in)
	if got["a"] != "given" || got["b"] != false || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if len(in) != 1 || strings.Contains(fmt.Sprint(in), "b:") {
		t.Fatalf("input modified: %v", in)
	}
}
