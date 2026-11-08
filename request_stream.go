package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Messages a server sends on an in-flight request's own response stream.
//
// While a request runs, the server may send it JSON-RPC notifications (and,
// for elicitation, requests) before the response. Where they go depends on
// the transport, and every revision agrees on the rule:
//
//   - Streamable HTTP: on the request's own response, which must therefore
//     be an SSE stream (2025-11-25 §Sending Messages 5: the server MAY send
//     JSON-RPC requests and notifications before the response, and they
//     SHOULD relate to the originating request; 2026-07-28 §Sending Messages
//     6). In 2026-07-28 a request-scoped notification is never sent on a
//     subscriptions/listen stream.
//   - stdio: written to stdout, interleaved with other messages.
//
// requestStream is that channel for one request, carried on the handler's
// context. The transport supplies send; after finish (called before the
// response is written, or when the request is cancelled) nothing more is
// sent, so a handler goroutine that outlives its request can never put a
// message on the wire after the response (progress: notifications MUST stop
// after completion; cancellation: the server MUST NOT send further messages
// for a cancelled request).

var (
	// ErrRequestCompleted is returned when a handler sends a message for a
	// request that has already been answered or cancelled.
	ErrRequestCompleted = errors.New("request already completed")
	// ErrProgressNotIncreasing is returned by ReportProgress for a value
	// that is not greater than the last one reported: the spec requires
	// progress to increase with each notification.
	ErrProgressNotIncreasing = errors.New("progress must increase with each notification")
)

// DefaultProgressInterval is the default minimum time between two progress
// notifications for one request; see SetProgressInterval.
const DefaultProgressInterval = 10 * time.Millisecond

// errStreamUnavailable means the transport cannot carry a message for this
// request (an HTTP client that did not accept text/event-stream, response
// streaming disabled, a batched request) or its buffer is full; the message
// was dropped.
var errStreamUnavailable = errors.New("request stream unavailable")

type requestStreamKey struct{}

// requestStream is the outbound channel to one in-flight request; see the
// comment at the top of this file.
type requestStream struct {
	mu sync.Mutex
	// send writes one encoded JSON-RPC message for the request, reporting
	// false when it was dropped. nil when the transport cannot carry
	// messages for this request at all.
	send func(msg []byte) bool
	done bool

	// Progress state (see ReportProgress): the request's token (a string or
	// a json.Number integer, nil when the client sent none), the last value
	// reported, and when the last notification was sent.
	progressToken    any
	progressInterval time.Duration
	progressReported bool
	progressLast     float64
	progressSentAt   time.Time
}

// newRequestStream creates the stream for one request. params is the
// request's raw params (for _meta.progressToken); send may be nil.
func (s *Server) newRequestStream(params json.RawMessage, send func([]byte) bool) *requestStream {
	return &requestStream{
		send:             send,
		progressToken:    progressTokenFrom(params),
		progressInterval: s.progressIntervalConfig(),
	}
}

func withRequestStream(ctx context.Context, rs *requestStream) context.Context {
	return context.WithValue(ctx, requestStreamKey{}, rs)
}

func requestStreamFrom(ctx context.Context) *requestStream {
	rs, _ := ctx.Value(requestStreamKey{}).(*requestStream)
	return rs
}

// finish closes the stream: nothing is sent after it returns. Idempotent.
// Sends happen under rs.mu, so a send already in progress completes first.
func (rs *requestStream) finish() {
	rs.mu.Lock()
	rs.done = true
	rs.mu.Unlock()
}

// notifyLocked encodes and sends one notification; rs.mu must be held.
func (rs *requestStream) notifyLocked(method string, params any) error {
	if rs.done {
		return ErrRequestCompleted
	}
	if rs.send == nil {
		return errStreamUnavailable
	}
	line, err := json.Marshal(MCPNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	if !rs.send(line) {
		return errStreamUnavailable
	}
	return nil
}

// notify sends one notification on the stream (notifications/cancelled for
// a server-to-client request the server gives up on).
func (rs *requestStream) notify(method string, params any) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.notifyLocked(method, params)
}

// sendRequest sends a server-to-client JSON-RPC request (elicitation/create)
// related to this request; the client's response is routed back by id
// (elicitation.go).
func (rs *requestStream) sendRequest(id any, method string, params any) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.done {
		return ErrRequestCompleted
	}
	if rs.send == nil {
		return errStreamUnavailable
	}
	line, err := json.Marshal(MCPRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	if !rs.send(line) {
		return errStreamUnavailable
	}
	return nil
}

// progressTokenFrom reads params._meta.progressToken. The spec allows a
// string or an integer; anything else is not a token, and the request is
// treated as having asked for no progress. Integers are kept as json.Number
// so the token is echoed exactly, however large.
func progressTokenFrom(params json.RawMessage) any {
	if len(params) == 0 {
		return nil
	}
	var p struct {
		Meta struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.Meta.ProgressToken) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(p.Meta.ProgressToken))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		if _, err := t.Int64(); err == nil {
			return t
		}
	}
	return nil
}

// SetProgressInterval sets the minimum time between two progress
// notifications for one request (the spec: both parties SHOULD rate limit to
// prevent flooding). A report arriving sooner is dropped, unless it reaches
// the total, so the final step is always delivered. 0 disables the limit;
// a negative value restores DefaultProgressInterval.
func (s *Server) SetProgressInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progressIntervalSet = d >= 0
	s.progressInterval = d
}

func (s *Server) progressIntervalConfig() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.progressIntervalSet {
		return DefaultProgressInterval
	}
	return s.progressInterval
}

// ProgressToken returns the progress token of the request ctx belongs to,
// and whether the client sent one. The token is a string or a json.Number.
func ProgressToken(ctx context.Context) (any, bool) {
	rs := requestStreamFrom(ctx)
	if rs == nil || rs.progressToken == nil {
		return nil, false
	}
	return rs.progressToken, true
}

// ReportProgress sends notifications/progress for the request ctx belongs
// to. Call it from a tool, prompt or resource handler with that handler's
// context (or one derived from it); tools reached through execute_tool and
// provider tools get the same context.
//
// total is the expected final value, or 0 when unknown (it is then
// omitted); message is optional human-readable status. The spec's rules are
// enforced here:
//
//   - No progressToken on the request: nothing is sent, and nil is returned.
//   - progress MUST increase with each notification: a value not greater
//     than the last one reported returns ErrProgressNotIncreasing.
//   - Notifications MUST stop after completion: once the request has been
//     answered or cancelled this returns ErrRequestCompleted.
//   - Rate limiting (see SetProgressInterval): a report sooner than the
//     interval after the last one is dropped and nil is returned, unless
//     progress has reached total.
//
// A transport that cannot carry the notification (an HTTP client whose
// Accept header does not list text/event-stream, or streaming disabled with
// SetResponseStreaming) drops it silently; progress is advisory. On HTTP
// the first notification switches the response to an SSE stream at once.
func ReportProgress(ctx context.Context, progress, total float64, message string) error {
	rs := requestStreamFrom(ctx)
	if rs == nil || rs.progressToken == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return ErrRequestCompleted // cancelled: send nothing further
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.done {
		return ErrRequestCompleted
	}
	if rs.progressReported && progress <= rs.progressLast {
		return ErrProgressNotIncreasing
	}
	rs.progressReported = true
	rs.progressLast = progress
	final := total > 0 && progress >= total
	now := time.Now()
	if !rs.progressSentAt.IsZero() && !final && now.Sub(rs.progressSentAt) < rs.progressInterval {
		return nil // rate limited
	}
	params := map[string]any{"progressToken": rs.progressToken, "progress": progress}
	if total > 0 {
		params["total"] = total
	}
	if message != "" {
		params["message"] = message
	}
	switch err := rs.notifyLocked("notifications/progress", params); err {
	case nil:
		rs.progressSentAt = now
		return nil
	case errStreamUnavailable:
		return nil
	default:
		return err
	}
}
