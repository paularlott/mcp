package mcp

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Streamed (SSE) responses for long-running requests on the Streamable HTTP
// transport.
//
// Both eras let a server answer a POSTed JSON-RPC request with either
// Content-Type application/json or text/event-stream, and require clients to
// support both (2025-11-25 §Sending Messages 5; 2026-07-28 §Sending
// Messages 6). A plain JSON response sends nothing until the handler
// finishes, so any intermediary with a read timeout (Cloudflare's ~100s
// origin timeout, nginx's proxy_read_timeout, a load balancer idle timeout)
// kills a slow tool call before its result arrives.
//
// For the methods that run user code (tools/call, resources/read,
// prompts/get), when the client's Accept header lists text/event-stream:
//
//   - The handler runs, and if it finishes within the streaming delay the
//     response is written as application/json exactly as before — fast
//     calls are unchanged on the wire.
//   - Otherwise the server opens an SSE stream (200, text/event-stream,
//     Cache-Control: no-cache, X-Accel-Buffering: no as 2026-07-28 advises)
//     and writes an SSE comment (": ping") every keep-alive interval. Comments
//     carry no event and clients must ignore them (WHATWG SSE; 2026-07-28
//     explicitly encourages them as keep-alives), but they reset every
//     intermediary's idle timer.
//   - The JSON-RPC response is sent as a single "message" event, after which
//     the stream is terminated (both eras: the response SHOULD terminate it).
//   - A client disconnect cancels the request's context, stops the stream
//     and sends nothing further (2026-07-28: closing the response stream
//     MUST be treated as cancellation and the server MUST NOT send further
//     messages for it).
//
// Deliberately not done:
//
//   - No SSE event ids or "priming" event. 2025-11-25 says a server SHOULD
//     prime the stream with an event id so the client can reconnect with
//     Last-Event-ID, but that only makes sense for a resumable stream; this
//     server does not replay (and 2026-07-28 removes resumability entirely).
//     Handing out an id would invite a reconnect that can never be served.
//   - Legacy disconnect semantics: 2025-11-25 says a disconnect SHOULD NOT
//     be read as cancellation. The request context is still cancelled on
//     disconnect, as it always has been for the JSON path: with no
//     resumability the result has nowhere to go, so continuing the work
//     would only waste it.
//   - Headers a handler sets are only carried on the JSON path; once the
//     stream has started, headers are already on the wire. None of the
//     streamable methods set response headers.

const (
	// DefaultStreamingDelay is how long a streamable request may run before
	// its response switches from application/json to an SSE stream.
	DefaultStreamingDelay = 2 * time.Second

	// DefaultStreamingKeepAlive is the interval between SSE keep-alive
	// comments on a streamed response. Well under common intermediary idle
	// timeouts (Cloudflare ~100s, nginx proxy_read_timeout 60s).
	DefaultStreamingKeepAlive = 15 * time.Second
)

// streamableMethods are the methods whose responses may be streamed: the ones
// that execute user handlers of unbounded duration. initialize is excluded
// deliberately — it sets the MCP-Session-Id response header, which must be
// on the response's own headers.
var streamableMethods = map[string]bool{
	"tools/call":     true,
	"resources/read": true,
	"prompts/get":    true,
}

// SetResponseStreaming configures streamed responses for long-running
// requests (see server_stream.go). delay is how long a request may run before
// the response switches to an SSE stream; 0 streams immediately and a
// negative value disables streaming (every response is application/json).
// keepAlive is the interval between keep-alive comments; <= 0 uses
// DefaultStreamingKeepAlive. Defaults apply when this is never called.
func (s *Server) SetResponseStreaming(delay, keepAlive time.Duration) {
	if keepAlive <= 0 {
		keepAlive = DefaultStreamingKeepAlive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamingConfigured = true
	s.streamingDelay = delay
	s.streamingKeepAlive = keepAlive
}

func (s *Server) streamingConfig() (delay, keepAlive time.Duration) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.streamingConfigured {
		return DefaultStreamingDelay, DefaultStreamingKeepAlive
	}
	return s.streamingDelay, s.streamingKeepAlive
}

// acceptsEventStream reports whether an Accept header explicitly lists
// text/event-stream with a non-zero quality. Wildcards (*/*, text/*) are not
// taken as consent: a client that has not named SSE may not parse it.
func acceptsEventStream(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || mediaType != "text/event-stream" {
			continue
		}
		if q, ok := params["q"]; ok {
			if v, err := strconv.ParseFloat(q, 64); err != nil || v <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

// capturedResponse is a complete response ready to write: status, headers
// and a JSON-RPC body.
type capturedResponse struct {
	status int
	header http.Header
	body   []byte
}

func (c capturedResponse) writeJSON(w http.ResponseWriter) {
	for k, v := range c.header {
		w.Header()[k] = v
	}
	w.WriteHeader(c.status)
	w.Write(c.body)
}

// legacyShape passes a captured Legacy response through unchanged.
func legacyShape(_ string, capture *modernResponseCapture) capturedResponse {
	return capturedResponse{status: capture.status, header: capture.header, body: capture.body.Bytes()}
}

// dispatchResponse runs dispatchMethod for req and writes its response,
// streaming it as SSE when the method, client and ResponseWriter allow and
// the handler outlives the streaming delay. shape turns the handler's
// captured output into the era's final response.
func (s *Server) dispatchResponse(w http.ResponseWriter, r *http.Request, req *MCPRequest, shape func(method string, capture *modernResponseCapture) capturedResponse) {
	delay, keepAlive := s.streamingConfig()
	flusher, canFlush := w.(http.Flusher)
	if delay < 0 || !canFlush || !streamableMethods[req.Method] || !acceptsEventStream(r.Header.Get("Accept")) {
		capture := newModernResponseCapture()
		s.dispatchMethod(capture, r, req)
		shape(req.Method, capture).writeJSON(w)
		return
	}

	// The handler writes only into capture, and only this goroutine touches
	// w, so the two never race. done's close publishes capture.
	capture := newModernResponseCapture()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			// A panic here would otherwise kill the process: it is no longer
			// on net/http's goroutine, whose recover used to catch it.
			if p := recover(); p != nil {
				capture = newModernResponseCapture()
				s.sendMCPError(capture, req.ID, ErrorCodeInternalError, "Internal error", map[string]any{"details": fmt.Sprint(p)})
			}
		}()
		s.dispatchMethod(capture, r, req)
	}()

	timer := time.NewTimer(delay)
	select {
	case <-done:
		timer.Stop()
		shape(req.Method, capture).writeJSON(w)
		return
	case <-r.Context().Done():
		timer.Stop()
		<-done
		return
	case <-timer.C:
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			// A non-200 status the handler chose (a Modern protocol error)
			// cannot be sent now; the JSON-RPC error body still says it all.
			writeSSEMessage(w, shape(req.Method, capture).body)
			flusher.Flush()
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				<-done
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			// Cancelled (client closed the stream): send nothing further.
			<-done
			return
		}
	}
}

// writeSSEMessage writes body as one SSE "message" event. JSON encoding never
// emits raw newlines inside a value, but any line break in body is split
// across data lines so the event is well formed regardless.
func writeSSEMessage(w http.ResponseWriter, body []byte) error {
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = bytes.ReplaceAll(body, []byte("\r"), []byte("\n"))
	body = bytes.TrimRight(body, "\n")
	var buf bytes.Buffer
	for _, line := range bytes.Split(body, []byte("\n")) {
		buf.WriteString("data: ")
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}
