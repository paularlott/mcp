package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Without session management a Legacy HTTP server can't tell its clients
// apart, so it never elicits there: a client that declared elicitation gets
// ErrElicitationUnsupported, not a question another client could answer.
func TestElicitLegacyHTTP_RequiresSession(t *testing.T) {
	s := NewServer("no-sessions", "1")
	s.SetResponseStreaming(time.Minute, time.Minute)
	s.RegisterTool(NewTool("ask", "asks"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		_, err := Elicit(ctx, ElicitForm{Message: "Your name?", Schema: testNameSchema})
		if errors.Is(err, ErrElicitationUnsupported) {
			return NewToolResponseText("unsupported"), nil
		}
		if err != nil {
			return nil, err
		}
		return NewToolResponseText("asked"), nil
	})
	srv := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer srv.Close()

	c := &legacyHTTPClient{t: t, url: srv.URL, version: "2025-11-25"}
	resp := c.post(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": elicitCaps, "clientInfo": map[string]any{"name": "t", "version": "1"},
	}}, "application/json")
	resp.Body.Close()
	if sid := resp.Header.Get(headerSessionID); sid != "" {
		t.Fatalf("a server without session management issued session %q", sid)
	}
	msgs := c.call(2, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, sseAccept)
	if text, isErr := toolText(t, nextMessage(t, msgs)); isErr || text != "unsupported" {
		t.Fatalf("got %q (isError %v), want unsupported", text, isErr)
	}
}

// A response is accepted only from the principal the question went to: a
// different authenticated caller who has the session id and the request id
// is ignored, and the real user's answer still arrives.
func TestElicitLegacyHTTP_AnswerBoundToPrincipal(t *testing.T) {
	s := newElicitServer(t, nil)
	// Stand-in for an auth middleware: the user comes from the query.
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithTokenInfo(r.Context(), &TokenInfo{Subject: r.URL.Query().Get("user")})
		s.HandleRequest(w, r.WithContext(ctx))
	})
	srv := httptest.NewServer(authed)
	defer srv.Close()

	alice := newLegacyHTTPClient(t, srv.URL+"?user=alice", "2025-11-25", elicitCaps)
	msgs := alice.call(2, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, sseAccept)
	question := nextMessage(t, msgs)
	if question["method"] != "elicitation/create" {
		t.Fatalf("want elicitation/create, got %v", question)
	}

	// Bob has stolen alice's session id and the request id.
	bob := *alice
	bob.url = srv.URL + "?user=bob"
	bob.respond(question["id"], map[string]any{"action": "accept", "content": map[string]any{"name": "Mallory"}})

	alice.respond(question["id"], map[string]any{"action": "accept", "content": map[string]any{"name": "Alice"}})
	if text, isErr := toolText(t, nextMessage(t, msgs)); isErr || text != "accept Alice" {
		t.Fatalf("got %q (isError %v), want alice's own answer", text, isErr)
	}
}

// Server-to-client request ids are random, so they can't be guessed by
// anyone who hasn't seen the question.
func TestServerRequestIDsAreRandom(t *testing.T) {
	format := regexp.MustCompile(`^elicitation-[0-9a-f]{32}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newServerRequestID()
		if !format.MatchString(id) {
			t.Fatalf("id %q is not elicitation-<128-bit hex>", id)
		}
		if seen[id] {
			t.Fatalf("id %q repeated", id)
		}
		seen[id] = true
	}
	if strings.HasSuffix(newServerRequestID(), "-1") {
		t.Fatal("ids look sequential")
	}
}
