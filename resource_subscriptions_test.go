package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Resource subscriptions on every revision: Legacy resources/subscribe and
// resources/unsubscribe (per HTTP session, per stdio connection) and the
// Modern subscriptions/listen resourceSubscriptions filter.

// legacyGetStream opens a Legacy session's standalone GET SSE stream.
func legacyGetStream(t *testing.T, s *Server, url, sid string) (*sseLineReader, context.CancelFunc) {
	t.Helper()
	before := s.notifications.count()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(headerProtocolVersion, "2025-11-25")
	req.Header.Set(headerSessionID, sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET status %d", resp.StatusCode)
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	// The stream registers after its headers are flushed; wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for s.notifications.count() <= before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return newSSELineReader(resp.Body), cancel
}

// legacySubscribe sends resources/subscribe or resources/unsubscribe and
// returns the decoded response.
func legacySubscribe(t *testing.T, url, sid, method, uri string) map[string]any {
	t.Helper()
	resp, raw := legacyPost(t, url, "2025-11-25", sid,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":%q,"params":{"uri":%q}}`, method, uri))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status %d: %s", method, resp.StatusCode, raw)
	}
	return decodeMCPResponse(t, raw)
}

// eventChan pumps a stream's data: events into a channel.
func eventChan(t *testing.T, r *sseLineReader) <-chan string {
	ch := make(chan string, 16)
	go func() {
		defer close(ch)
		for {
			ev, ok := tryNext(r)
			if !ok {
				return
			}
			ch <- ev
		}
	}()
	return ch
}

// tryNext is sseLineReader.next without failing the test on a closed stream.
func tryNext(r *sseLineReader) (string, bool) {
	chunk := make([]byte, 4096)
	for {
		for {
			idx := strings.Index(r.buf, "\n\n")
			if idx < 0 {
				break
			}
			event := r.buf[:idx+2]
			r.buf = r.buf[idx+2:]
			if strings.HasPrefix(event, "data: ") {
				return strings.TrimSpace(strings.TrimPrefix(event, "data: ")), true
			}
		}
		n, err := r.r.Read(chunk)
		if n > 0 {
			r.buf += string(chunk[:n])
			continue
		}
		if err != nil {
			return "", false
		}
	}
}

func expectEvent(t *testing.T, ch <-chan string, want string) string {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("stream closed, want %s", want)
		}
		if !strings.Contains(ev, want) {
			t.Fatalf("event = %s, want it to contain %s", ev, want)
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("no event, want %s", want)
	}
	return ""
}

func expectNoEvent(t *testing.T, ch <-chan string, wait time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event: %s", ev)
		}
	case <-time.After(wait):
	}
}

// The capability flag: resources.subscribe is true wherever the server can
// honour a subscription — Legacy HTTP with session management, Legacy stdio,
// and every Modern client — and false on Legacy HTTP without sessions, which
// cannot tie a subscription to a stream.
func TestResourceSubscribeCapability(t *testing.T) {
	subscribeFlag := func(t *testing.T, raw []byte) any {
		t.Helper()
		out := decodeMCPResponse(t, raw)
		result, _ := out["result"].(map[string]any)
		caps, _ := result["capabilities"].(map[string]any)
		res, _ := caps["resources"].(map[string]any)
		return res["subscribe"]
	}

	t.Run("legacy http with sessions", func(t *testing.T) {
		_, ts := legacyTestServer(t, true)
		_, raw := legacyPost(t, ts.URL, "", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
		if got := subscribeFlag(t, raw); got != true {
			t.Fatalf("resources.subscribe = %v, want true", got)
		}
	})
	t.Run("legacy http without sessions", func(t *testing.T) {
		_, ts := legacyTestServer(t, false)
		_, raw := legacyPost(t, ts.URL, "", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
		if got := subscribeFlag(t, raw); got != false {
			t.Fatalf("resources.subscribe = %v, want false", got)
		}
		// And the methods are unknown there, matching the capability.
		resp, raw := legacyPost(t, ts.URL, "2025-11-25", "", `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"test://r"}}`)
		out := decodeMCPResponse(t, raw)
		errObj, _ := out["error"].(map[string]any)
		if resp.StatusCode != http.StatusOK || errObj["code"] != float64(ErrorCodeMethodNotFound) {
			t.Fatalf("subscribe without sessions = %d %s, want -32601", resp.StatusCode, raw)
		}
	})
	t.Run("legacy stdio", func(t *testing.T) {
		h := newStdioHarness(t, stdioToolServer(t))
		h.send(t, stdioInit2025)
		if l := h.next(t, 2*time.Second); !strings.Contains(l, `"subscribe":true`) {
			t.Fatalf("initialize = %s", l)
		}
	})
	t.Run("modern discover", func(t *testing.T) {
		s := NewServer("caps", "1")
		ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
		defer ts.Close()
		resp, err := http.DefaultClient.Do(modernRequest(t, ts.URL, "server/discover", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		result, _ := out["result"].(map[string]any)
		caps, _ := result["capabilities"].(map[string]any)
		res, _ := caps["resources"].(map[string]any)
		if res["subscribe"] != true {
			t.Fatalf("discover resources = %#v, want subscribe true", res)
		}
	})
}

// Legacy HTTP: subscribe and unsubscribe return {}; an update reaches the
// subscribed session's GET stream, never another session's, and stops after
// unsubscribe.
func TestLegacyHTTPResourceSubscribe(t *testing.T) {
	s, ts := legacyTestServer(t, true)
	a := legacyInitialize(t, ts.URL, "2025-11-25")
	b := legacyInitialize(t, ts.URL, "2025-11-25")
	streamA, _ := legacyGetStream(t, s, ts.URL, a)
	streamB, _ := legacyGetStream(t, s, ts.URL, b)
	evA, evB := eventChan(t, streamA), eventChan(t, streamB)

	out := legacySubscribe(t, ts.URL, a, "resources/subscribe", "test://watched")
	if r, ok := out["result"].(map[string]any); !ok || len(r) != 0 {
		t.Fatalf("subscribe result = %#v, want {}", out)
	}

	s.NotifyResourceUpdated("test://watched")
	ev := expectEvent(t, evA, `"method":"notifications/resources/updated"`)
	var msg MCPNotification
	if err := json.Unmarshal([]byte(ev), &msg); err != nil {
		t.Fatal(err)
	}
	if p, _ := msg.Params.(map[string]any); p["uri"] != "test://watched" || len(p) != 1 {
		t.Fatalf("params = %#v, want exactly {uri}", msg.Params)
	}
	expectNoEvent(t, evB, 200*time.Millisecond) // session B never subscribed

	s.NotifyResourceUpdated("test://other") // nobody subscribed to this URI
	expectNoEvent(t, evA, 200*time.Millisecond)

	out = legacySubscribe(t, ts.URL, a, "resources/unsubscribe", "test://watched")
	if r, ok := out["result"].(map[string]any); !ok || len(r) != 0 {
		t.Fatalf("unsubscribe result = %#v, want {}", out)
	}
	s.NotifyResourceUpdated("test://watched")
	expectNoEvent(t, evA, 200*time.Millisecond)

	// Unsubscribing from something never subscribed to is harmless.
	legacySubscribe(t, ts.URL, b, "resources/unsubscribe", "test://never")
}

// A subscription belongs to the session, not to one GET stream: it survives
// the stream reconnecting, nothing is delivered while no stream is open, and
// list-changed notifications still reach every stream.
func TestLegacyHTTPResourceSubscriptionOutlivesStream(t *testing.T) {
	s, ts := legacyTestServer(t, true)
	sid := legacyInitialize(t, ts.URL, "2025-11-25")
	legacySubscribe(t, ts.URL, sid, "resources/subscribe", "test://watched")

	stream, cancel := legacyGetStream(t, s, ts.URL, sid)
	ev := eventChan(t, stream)
	s.NotifyResourceUpdated("test://watched")
	expectEvent(t, ev, `"uri":"test://watched"`)

	cancel() // client closes the stream
	deadline := time.Now().Add(2 * time.Second)
	for s.notifications.count() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.NotifyResourceUpdated("test://watched") // no stream: nowhere to deliver
	expectNoEvent(t, ev, 100*time.Millisecond)

	stream2, _ := legacyGetStream(t, s, ts.URL, sid)
	ev2 := eventChan(t, stream2)
	s.NotifyResourceUpdated("test://watched")
	expectEvent(t, ev2, `"uri":"test://watched"`)
	s.NotifyToolsChanged()
	expectEvent(t, ev2, NotificationToolsChanged)
}

// A terminated session's subscriptions are dropped with it.
func TestLegacyHTTPResourceSubscriptionDroppedOnDelete(t *testing.T) {
	s := NewServer("del", "1")
	s.SetSessionManager(newMockSessionManager())
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()
	sid := legacyInitialize(t, ts.URL, "2025-11-25")
	legacySubscribe(t, ts.URL, sid, "resources/subscribe", "test://watched")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set(headerSessionID, sid)
	req.Header.Set(headerProtocolVersion, "2025-11-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status %d", resp.StatusCode)
	}
	s.notifications.mu.RLock()
	_, kept := s.notifications.sessionResources[sid]
	s.notifications.mu.RUnlock()
	if kept {
		t.Fatal("subscriptions kept after the session was deleted")
	}
}

// Idle sessions with no stream are pruned; streaming ones are kept.
func TestLegacyHTTPResourceSubscriptionIdlePrune(t *testing.T) {
	h := newNotificationHub()
	h.subscribeSessionResource("idle", "test://r")
	h.subscribeSessionResource("live", "test://r")
	h.subscribeForSession("live", newSSESink(1))
	h.mu.Lock()
	for _, sr := range h.sessionResources {
		sr.lastSeen = time.Now().Add(-2 * resourceSubscriptionIdleTTL)
	}
	h.lastResourcePrune = time.Time{}
	h.mu.Unlock()
	h.subscribeSessionResource("new", "test://r") // triggers a prune
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.sessionResources["idle"]; ok {
		t.Fatal("idle session without a stream was not pruned")
	}
	if _, ok := h.sessionResources["live"]; !ok {
		t.Fatal("session with an open stream was pruned")
	}
}

// Malformed params, and the per-subscriber bound.
func TestLegacyHTTPResourceSubscribeValidation(t *testing.T) {
	_, ts := legacyTestServer(t, true)
	sid := legacyInitialize(t, ts.URL, "2025-11-25")
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/subscribe","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/subscribe","params":{"uri":42}}`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/unsubscribe"}`,
	} {
		_, raw := legacyPost(t, ts.URL, "2025-11-25", sid, body)
		errObj, _ := decodeMCPResponse(t, raw)["error"].(map[string]any)
		if errObj["code"] != float64(ErrorCodeInvalidParams) {
			t.Fatalf("%s -> %s, want -32602", body, raw)
		}
	}

	h := newNotificationHub()
	for i := 0; i < maxResourceSubscriptions; i++ {
		if !h.subscribeSessionResource("s", fmt.Sprintf("test://%d", i)) {
			t.Fatalf("subscription %d refused below the limit", i)
		}
	}
	if h.subscribeSessionResource("s", "test://one-too-many") {
		t.Fatal("subscription beyond the limit accepted")
	}
	if !h.subscribeSessionResource("s", "test://0") {
		t.Fatal("re-subscribing to a held URI refused at the limit")
	}
}

// On the Modern path resources/subscribe and resources/unsubscribe do not
// exist (2026-07-28 removed them): 404 with -32601.
func TestModernResourcesSubscribeIsMethodNotFound(t *testing.T) {
	s := NewServer("modern", "1")
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()
	for _, method := range []string{"resources/subscribe", "resources/unsubscribe"} {
		resp, err := http.DefaultClient.Do(modernRequest(t, ts.URL, method, map[string]any{"uri": "test://r"}))
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		errObj, _ := out["error"].(map[string]any)
		if resp.StatusCode != http.StatusNotFound || errObj["code"] != float64(ErrorCodeMethodNotFound) {
			t.Fatalf("%s = %d %#v, want 404 / -32601", method, resp.StatusCode, out)
		}
	}
}

// Legacy stdio: the connection is the session. Updates are written only for
// subscribed URIs, only to the connection that subscribed, and stop after
// unsubscribe or when the connection ends.
func TestStdioResourceSubscribe(t *testing.T) {
	s := stdioToolServer(t)
	a := newStdioHarness(t, s)
	b := newStdioHarness(t, s)
	for _, h := range []*stdioHarness{a, b} {
		h.send(t, stdioInit2025)
		h.next(t, 2*time.Second)
	}

	a.send(t, `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"file:///watched"}}`)
	if l := a.next(t, 2*time.Second); !isEmptyResult(l, 2) {
		t.Fatalf("subscribe = %s", l)
	}

	s.NotifyResourceUpdated("file:///watched")
	if l := a.next(t, 2*time.Second); l != `{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"file:///watched"}}` {
		t.Fatalf("update = %s", l)
	}
	b.none(t, 200*time.Millisecond) // the other connection never subscribed
	s.NotifyResourceUpdated("file:///other")
	a.none(t, 200*time.Millisecond)

	a.send(t, `{"jsonrpc":"2.0","id":3,"method":"resources/unsubscribe","params":{"uri":"file:///watched"}}`)
	if l := a.next(t, 2*time.Second); !isEmptyResult(l, 3) {
		t.Fatalf("unsubscribe = %s", l)
	}
	s.NotifyResourceUpdated("file:///watched")
	a.none(t, 200*time.Millisecond)

	// After the connection ends, its subscriptions go with it.
	b.send(t, `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"file:///watched"}}`)
	b.next(t, 2*time.Second)
	before := s.notifications.count()
	b.in.Close()
	if err := <-b.done; err != nil {
		t.Fatalf("ServeStream: %v", err)
	}
	if s.notifications.count() != before-1 {
		t.Fatalf("connection's sink still registered after it ended")
	}
	s.NotifyResourceUpdated("file:///watched") // must not panic or reach anyone
}

// isEmptyResult reports whether line is a JSON-RPC response to id whose
// result is {}.
func isEmptyResult(line string, id float64) bool {
	var resp struct {
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	return json.Unmarshal([]byte(line), &resp) == nil && resp.ID == id && string(resp.Result) == "{}"
}

// On stdio a Modern request for resources/subscribe is Method not found.
func TestStdioModernResourcesSubscribeIsMethodNotFound(t *testing.T) {
	h := newStdioHarness(t, stdioToolServer(t))
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"resources/subscribe","params":{"uri":"file:///x",`+meta+`}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"code":-32601`) {
		t.Fatalf("got %s, want -32601", l)
	}
}

// Modern HTTP: resourceSubscriptions is echoed in the acknowledgement (in
// order, deduplicated, blanks dropped); updates carry uri and the stream's
// subscriptionId, and only reach streams that listed that URI.
func TestSubscriptionsListen_ResourceSubscriptions(t *testing.T) {
	s := NewServer("subs-res", "1")
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	t.Cleanup(ts.Close) // after the streams' own cleanups close them

	open := func(notifications map[string]any) (<-chan string, string, context.CancelFunc) {
		t.Helper()
		before := s.notifications.count()
		ctx, cancel := context.WithCancel(context.Background())
		req := modernRequest(t, ts.URL, "subscriptions/listen", map[string]any{"notifications": notifications})
		resp, err := http.DefaultClient.Do(req.WithContext(ctx))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("status = %d", resp.StatusCode)
		}
		t.Cleanup(func() { cancel(); resp.Body.Close() })
		ch := eventChan(t, newSSELineReader(resp.Body))
		ack := expectEvent(t, ch, "notifications/subscriptions/acknowledged")
		waitForSubscribers(t, s, before+1)
		return ch, ack, cancel
	}

	watched, ack, cancelWatched := open(map[string]any{
		"resourceSubscriptions": []any{"test://a", "", "test://b", "test://a"},
	})
	var ackMsg struct {
		Params struct {
			Meta          map[string]any `json:"_meta"`
			Notifications map[string]any `json:"notifications"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(ack), &ackMsg); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(ackMsg.Params.Notifications)
	if string(got) != `{"resourceSubscriptions":["test://a","test://b"]}` {
		t.Fatalf("acked = %s", got)
	}
	toolsOnly, _, _ := open(map[string]any{"toolsListChanged": true})

	s.NotifyResourceUpdated("test://b")
	ev := expectEvent(t, watched, `"method":"notifications/resources/updated"`)
	var msg struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(ev), &msg); err != nil {
		t.Fatal(err)
	}
	meta, _ := msg.Params["_meta"].(map[string]any)
	if msg.Params["uri"] != "test://b" || meta[metaKeySubscriptionID] != float64(1) {
		t.Fatalf("update = %s, want uri test://b and subscriptionId 1", ev)
	}
	expectNoEvent(t, toolsOnly, 200*time.Millisecond) // didn't list the URI

	s.NotifyResourceUpdated("test://c") // listed by nobody
	expectNoEvent(t, watched, 200*time.Millisecond)

	// A resource-only stream gets no list-changed notifications, and the
	// tools-only stream still does.
	s.NotifyToolsChanged()
	expectEvent(t, toolsOnly, NotificationToolsChanged)
	expectNoEvent(t, watched, 200*time.Millisecond)

	// Once the client closes the stream, nothing more is delivered to it.
	before := s.notifications.count()
	cancelWatched()
	deadline := time.Now().Add(2 * time.Second)
	for s.notifications.count() != before-1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.notifications.count() != before-1 {
		t.Fatal("closed listen stream still registered")
	}
	s.NotifyResourceUpdated("test://a")
	expectNoEvent(t, toolsOnly, 100*time.Millisecond)
}

// A wrongly typed resourceSubscriptions is Invalid params.
func TestSubscriptionsListen_ResourceSubscriptionsMalformed(t *testing.T) {
	for _, filter := range []string{
		`{"notifications":{"resourceSubscriptions":"test://a"}}`,
		`{"notifications":{"resourceSubscriptions":[1,2]}}`,
	} {
		if _, details := parseListenFilter(json.RawMessage(filter)); details == "" {
			t.Fatalf("%s accepted", filter)
		}
	}
	many := make([]string, maxResourceSubscriptions+5)
	for i := range many {
		many[i] = fmt.Sprintf("test://%d", i)
	}
	raw, _ := json.Marshal(map[string]any{"notifications": map[string]any{"resourceSubscriptions": many}})
	f, details := parseListenFilter(raw)
	if details != "" || len(f.resources) != maxResourceSubscriptions || len(f.acked["resourceSubscriptions"].([]string)) != maxResourceSubscriptions {
		t.Fatalf("over-long filter: details %q, honoured %d", details, len(f.resources))
	}
}

// Modern stdio: several listen subscriptions share the connection; each
// update is tagged with the subscriptionId of the one that listed the URI,
// and cancelling a subscription stops its updates.
func TestStdioModernResourceSubscriptions(t *testing.T) {
	s := stdioToolServer(t)
	h := newStdioHarness(t, s)
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

	h.send(t, `{"jsonrpc":"2.0","id":"r1","method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":["file:///a"]},`+meta+`}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"resourceSubscriptions":["file:///a"]`) || !strings.Contains(l, `"io.modelcontextprotocol/subscriptionId":"r1"`) {
		t.Fatalf("ack r1 = %s", l)
	}
	h.send(t, `{"jsonrpc":"2.0","id":"r2","method":"subscriptions/listen","params":{"notifications":{"resourceSubscriptions":["file:///b"]},`+meta+`}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"io.modelcontextprotocol/subscriptionId":"r2"`) {
		t.Fatalf("ack r2 = %s", l)
	}

	s.NotifyResourceUpdated("file:///b")
	l := h.next(t, 2*time.Second)
	if !strings.Contains(l, `"method":"notifications/resources/updated"`) || !strings.Contains(l, `"uri":"file:///b"`) || !strings.Contains(l, `"io.modelcontextprotocol/subscriptionId":"r2"`) {
		t.Fatalf("update = %s", l)
	}
	h.none(t, 200*time.Millisecond) // r1 did not list file:///b

	// A Modern connection never gets Legacy-style untagged updates.
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"r2"}}`)
	time.Sleep(100 * time.Millisecond)
	s.NotifyResourceUpdated("file:///b")
	h.none(t, 200*time.Millisecond)

	s.NotifyResourceUpdated("file:///a")
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"io.modelcontextprotocol/subscriptionId":"r1"`) || !strings.Contains(l, `"uri":"file:///a"`) {
		t.Fatalf("update = %s", l)
	}
}
