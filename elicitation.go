package mcp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Elicitation (form mode): a server asking the user, through the client, for
// structured input while it handles a tools/call, prompts/get or
// resources/read. One handler API, Elicit, serves every revision; how the
// question reaches the client differs by era.
//
// 2025-06-18 and 2025-11-25 (client/elicitation): the server sends an
// elicitation/create request to the client and waits for its result,
// {action: accept|decline|cancel, content?}.
//
//   - The server MUST NOT send it unless the client declared the elicitation
//     capability, and only in a mode the client declared: an empty
//     elicitation object means form mode only (2025-11-25 §Capabilities).
//     URL mode is not implemented.
//   - Streamable HTTP: the request goes on the originating request's own
//     response stream, which switches to SSE as progress does
//     (server_stream.go); the client POSTs its JSON-RPC response separately
//     and gets 202. It is routed by id to the waiting handler within the same
//     session. The reply must reach the same process: deployments with
//     several instances need sticky routing for this.
//   - stdio: the request is a line on stdout; the response is a line on stdin.
//   - 2025-03-26 has no elicitation: Elicit returns ErrElicitationUnsupported.
//   - Timeouts (lifecycle §Timeouts): when the client does not answer within
//     the elicitation timeout the server SHOULD issue notifications/cancelled
//     for its request and stop waiting.
//
// 2026-07-28 (basic/patterns/mrtr): server-to-client requests are gone.
// tools/call, prompts/get and resources/read MAY instead answer with an
// InputRequiredResult {resultType: "input_required", inputRequests,
// requestState}; the client retries the same request with inputResponses
// and the echoed requestState. Elicit implements that by re-running the
// handler: an answer already in inputResponses (or carried in requestState
// from an earlier round) is returned at once; otherwise the handler is
// stopped with ErrElicitationPending and the request is answered with an
// InputRequiredResult listing every question still open.
//
//   - requestState is attacker-controlled. It is HMAC-SHA256 protected and
//     binds the method, a digest of the request's salient params (everything
//     but _meta, inputResponses and requestState), an expiry and the
//     authenticated principal (TokenInfo.Subject) when there is one; state
//     that fails any of these is rejected with -32602 (servers MUST reject
//     state that fails verification; SHOULD bind principal, TTL and request).
//     It carries the answers of earlier rounds, so a multi-round handler
//     sees every answer on its last run. It is signed, not encrypted: the
//     client can read the answers it gave.
//   - Servers MUST NOT send an inputRequest the client has not declared
//     support for: without the elicitation capability in the request's
//     _meta clientCapabilities, Elicit returns ErrElicitationUnsupported.
//   - A missing answer gets a new InputRequiredResult re-requesting it
//     (SHOULD, rather than an error); inputResponses entries the server did
//     not ask for are ignored (SHOULD ignore what it does not recognise).
//   - InputRequiredResult is only ever sent on tools/call, prompts/get and
//     resources/read (MUST NOT on any other request).

// ElicitAction is the user's response to an elicitation.
type ElicitAction string

const (
	// ElicitAccept: the user submitted the form; Content holds the answers.
	ElicitAccept ElicitAction = "accept"
	// ElicitDecline: the user explicitly declined.
	ElicitDecline ElicitAction = "decline"
	// ElicitCancel: the user dismissed the request without choosing.
	ElicitCancel ElicitAction = "cancel"
)

// ElicitForm is one form-mode elicitation.
type ElicitForm struct {
	// Key identifies the question. On 2026-07-28 it is the inputRequests key
	// the client answers under, so it must be the same every time the
	// handler runs for the same request. Leave it empty to number questions
	// in the order the handler asks them ("elicitation-1", "elicitation-2",
	// ...), which is deterministic as long as the handler is. Keys must be
	// unique within one request.
	Key string
	// Message is shown to the user.
	Message string
	// Schema is the requestedSchema: a JSON Schema object whose properties
	// are flat primitives (string, number, integer, boolean, optionally with
	// enum/oneOf, default, min/max, format) or arrays of enum strings for
	// multi-select. Nested objects are not allowed by the spec.
	Schema map[string]any
}

// ElicitResult is the client's answer to one elicitation.
type ElicitResult struct {
	Action ElicitAction
	// Content holds the submitted values when Action is ElicitAccept, nil
	// otherwise. It has been validated against the form's Schema.
	Content map[string]any
}

// Accepted reports whether the user submitted the form.
func (r *ElicitResult) Accepted() bool { return r != nil && r.Action == ElicitAccept }

var (
	// ErrElicitationUnsupported: elicitation cannot be used for this
	// request. The client did not declare form-mode elicitation, the
	// protocol revision has none (2025-03-26), the transport cannot carry
	// the request (an HTTP client that does not accept text/event-stream,
	// response streaming disabled, a batch), or the handler is not serving a
	// tools/call, prompts/get or resources/read.
	ErrElicitationUnsupported = errors.New("elicitation is not supported for this request")
	// ErrElicitationPending is returned by Elicit on 2026-07-28 when the
	// answer is not in the request yet. Return it from the handler (wrapped
	// or not): the request is answered with an InputRequiredResult and the
	// client retries it with the answer. The handler's own result is
	// discarded whenever a question is pending.
	ErrElicitationPending = errors.New("elicitation pending: the client is asked for input and will retry the request")
	// ErrElicitationTimeout: the client did not answer elicitation/create
	// within the elicitation timeout (see SetElicitationTimeout).
	ErrElicitationTimeout = errors.New("elicitation timed out waiting for the client")
	// ErrInvalidElicitationResult: the client's answer is malformed or does
	// not match the requested schema.
	ErrInvalidElicitationResult = errors.New("invalid elicitation result")
)

// ElicitationError is a JSON-RPC error the client answered elicitation/create
// with (2025-x).
type ElicitationError struct {
	Code    int
	Message string
	Data    any
}

func (e *ElicitationError) Error() string {
	return fmt.Sprintf("elicitation/create failed: %s (code %d)", e.Message, e.Code)
}

const (
	// DefaultElicitationTimeout bounds how long a 2025-x elicitation waits
	// for the client's response; a person is filling in a form.
	DefaultElicitationTimeout = 10 * time.Minute
	// DefaultRequestStateTTL is how long a 2026-07-28 requestState stays
	// valid after it is issued.
	DefaultRequestStateTTL = 10 * time.Minute
)

// SetElicitationTimeout sets how long a 2025-x elicitation waits for the
// client's answer before cancelling it (notifications/cancelled) and
// returning ErrElicitationTimeout. 0 waits until the request's own context
// ends; a negative value restores DefaultElicitationTimeout.
func (s *Server) SetElicitationTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.elicitTimeoutSet = d >= 0
	s.elicitTimeout = d
}

func (s *Server) elicitationTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.elicitTimeoutSet {
		return DefaultElicitationTimeout
	}
	return s.elicitTimeout
}

// SetRequestStateKey sets the HMAC key that protects 2026-07-28
// requestState. By default each process uses its own random key, so state
// issued by one instance is rejected by another: give every instance of a
// multi-instance deployment the same secret (32 random bytes or more). An
// empty key restores the random per-process key.
func (s *Server) SetRequestStateKey(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateKey = append([]byte(nil), key...)
}

// SetRequestStateTTL sets how long a 2026-07-28 requestState stays valid;
// <= 0 restores DefaultRequestStateTTL. A retry after it lapses is rejected,
// and the client starts the request again.
func (s *Server) SetRequestStateTTL(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateTTL = d
}

func (s *Server) requestStateConfig() (key []byte, ttl time.Duration) {
	s.mu.RLock()
	key, ttl = s.stateKey, s.stateTTL
	s.mu.RUnlock()
	if ttl <= 0 {
		ttl = DefaultRequestStateTTL
	}
	if len(key) > 0 {
		return key, ttl
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stateKey) == 0 {
		s.stateKey = make([]byte, 32)
		if _, err := rand.Read(s.stateKey); err != nil {
			panic("mcp: cannot generate requestState key: " + err.Error())
		}
	}
	return s.stateKey, ttl
}

// Elicit asks the user, through the client, to fill in one form, from a
// tool, prompt or resource handler (pass the handler's ctx or one derived
// from it; tools reached through execute_tool and provider tools work the
// same). It returns the user's action and, on accept, the content.
//
// On 2025-06-18 and 2025-11-25 it blocks until the client answers, the
// elicitation timeout passes (ErrElicitationTimeout) or ctx ends.
//
// On 2026-07-28 the handler is re-run on each retry of the request: when the
// answer is already there Elicit returns it at once; otherwise it returns
// ErrElicitationPending, which the handler must return, and the client is
// asked. Everything a handler does before Elicit therefore runs again on
// the retry, and must be safe to repeat; do side effects after the last
// Elicit. Keys must be deterministic (see ElicitForm.Key).
//
// ErrElicitationUnsupported means the client cannot be asked: carry on
// without the input or fail the request.
func Elicit(ctx context.Context, form ElicitForm) (*ElicitResult, error) {
	results, err := ElicitAll(ctx, form)
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// ElicitAll asks several questions at once. On 2026-07-28 every unanswered
// form goes into one InputRequiredResult, saving round trips; on 2025-x the
// forms are asked one after another. Results are in the order of forms.
func ElicitAll(ctx context.Context, forms ...ElicitForm) ([]*ElicitResult, error) {
	run := elicitationFrom(ctx)
	if run == nil {
		return nil, ErrElicitationUnsupported
	}
	if len(forms) == 0 {
		return nil, nil
	}
	prepared, err := run.prepare(forms)
	if err != nil {
		return nil, err
	}
	if run.mrtr != nil {
		return run.mrtr.elicit(prepared)
	}
	return run.legacy.elicit(ctx, prepared)
}

// ElicitationSupported reports whether Elicit can reach the client for the
// request ctx belongs to: the client declared form-mode elicitation, the
// revision has it, and (2025-x) the transport can carry the request.
func ElicitationSupported(ctx context.Context) bool {
	run := elicitationFrom(ctx)
	if run == nil {
		return false
	}
	if run.mrtr != nil {
		return formElicitationDeclared(run.mrtr.caps)
	}
	return run.legacy.supported()
}

type elicitationKey struct{}

func withElicitation(ctx context.Context, run *elicitationRun) context.Context {
	return context.WithValue(ctx, elicitationKey{}, run)
}

func elicitationFrom(ctx context.Context) *elicitationRun {
	run, _ := ctx.Value(elicitationKey{}).(*elicitationRun)
	return run
}

// elicitationRun is one handler run's elicitation state: exactly one of
// legacy (2025-x) or mrtr (2026-07-28) is set.
type elicitationRun struct {
	mu       sync.Mutex
	seq      int
	relaySeq int
	used     map[string]bool
	legacy   *legacyElicitation
	mrtr     *mrtrElicitation
}

// Slots: state a federated call (client_elicitation.go) keeps across the
// re-runs of a 2026-07-28 handler, inside this server's own signed
// requestState. A slot is named by the order of the federated calls in the
// handler ("relay-1", "relay-2", ...), deterministic like default keys.

// nextRelaySlot names the next federated call of this run.
func (run *elicitationRun) nextRelaySlot() string {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.relaySeq++
	return fmt.Sprintf("relay-%d", run.relaySeq)
}

// recall returns what an earlier round stored in slot (2026-07-28 only).
func (run *elicitationRun) recall(slot string) (json.RawMessage, bool) {
	if run == nil || run.mrtr == nil {
		return nil, false
	}
	m := run.mrtr
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dropped[slot] {
		return nil, false
	}
	if v, ok := m.stashed[slot]; ok {
		return v, true
	}
	v, ok := m.carriedSlots[slot]
	return v, ok
}

// remember stores v in slot for the next round, should the request be
// answered with an InputRequiredResult (2026-07-28 only).
func (run *elicitationRun) remember(slot string, v json.RawMessage) {
	if run == nil || run.mrtr == nil {
		return
	}
	m := run.mrtr
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stashed == nil {
		m.stashed = map[string]json.RawMessage{}
	}
	m.stashed[slot] = v
	delete(m.dropped, slot)
}

// drop forgets slot: its federated call finished.
func (run *elicitationRun) drop(slot string) {
	if run == nil || run.mrtr == nil {
		return
	}
	m := run.mrtr
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dropped == nil {
		m.dropped = map[string]bool{}
	}
	m.dropped[slot] = true
	delete(m.stashed, slot)
}

// preparedForm is a validated ElicitForm with its key and a JSON-normalised
// schema (so validation and the wire see the same values).
type preparedForm struct {
	key     string
	message string
	schema  map[string]any
}

func (run *elicitationRun) prepare(forms []ElicitForm) ([]preparedForm, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.used == nil {
		run.used = map[string]bool{}
	}
	out := make([]preparedForm, 0, len(forms))
	for _, f := range forms {
		key := f.Key
		if key == "" {
			run.seq++
			key = fmt.Sprintf("elicitation-%d", run.seq)
		}
		if run.used[key] {
			return nil, fmt.Errorf("elicitation key %q is already used in this request", key)
		}
		run.used[key] = true
		schema, err := normalizeElicitSchema(f.Schema)
		if err != nil {
			return nil, err
		}
		out = append(out, preparedForm{key: key, message: f.Message, schema: schema})
	}
	return out, nil
}

// params is the elicitation/create params for f. mode is sent from
// 2025-11-25 (where it was introduced, optional for form) onwards.
func (f preparedForm) params(withMode bool) map[string]any {
	p := map[string]any{"message": f.message, "requestedSchema": f.schema}
	if withMode {
		p["mode"] = "form"
	}
	return p
}

// normalizeElicitSchema checks a requestedSchema against the spec's
// restricted subset (a flat object of primitive properties) and returns it
// as plain decoded JSON.
func normalizeElicitSchema(schema map[string]any) (map[string]any, error) {
	if schema == nil {
		return nil, errors.New("elicitation schema is required")
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("elicitation schema cannot be encoded: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if t, ok := out["type"]; ok && t != "object" {
		return nil, errors.New(`elicitation schema must have "type": "object"`)
	}
	out["type"] = "object"
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		return nil, errors.New("elicitation schema needs a properties object")
	}
	for name, p := range props {
		pm, _ := p.(map[string]any)
		switch pm["type"] {
		case "string", "number", "integer", "boolean":
		case "array":
			items, _ := pm["items"].(map[string]any)
			if items == nil || (items["enum"] == nil && items["anyOf"] == nil) {
				return nil, fmt.Errorf("elicitation property %q: an array must be a multi-select with items.enum or items.anyOf", name)
			}
		default:
			return nil, fmt.Errorf("elicitation property %q: type must be string, number, integer, boolean or a multi-select array; elicitation schemas are flat", name)
		}
	}
	return out, nil
}

// formElicitationDeclared reports whether client capabilities allow a
// form-mode elicitation: an elicitation object that is empty (form only,
// 2025-11-25 §Capabilities), names form, or names neither mode (the
// 2025-06-18 shape).
func formElicitationDeclared(caps map[string]any) bool {
	e, ok := caps["elicitation"].(map[string]any)
	if !ok {
		return false
	}
	_, form := e["form"]
	_, url := e["url"]
	return form || !url
}

// parseElicitResult decodes and checks a client's ElicitResult: action is
// one of the three, and accepted content conforms to the form's schema.
func parseElicitResult(raw json.RawMessage, f preparedForm) (*ElicitResult, error) {
	var wire struct {
		Action  string          `json:"action"`
		Content json.RawMessage `json:"content"`
	}
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &wire) != nil {
		return nil, fmt.Errorf("%w for %q: not an object", ErrInvalidElicitationResult, f.key)
	}
	res := &ElicitResult{Action: ElicitAction(wire.Action)}
	switch res.Action {
	case ElicitAccept:
	case ElicitDecline, ElicitCancel:
		return res, nil // content only accompanies accept
	default:
		return nil, fmt.Errorf("%w for %q: action must be accept, decline or cancel", ErrInvalidElicitationResult, f.key)
	}
	content := map[string]any{}
	if len(wire.Content) > 0 && string(wire.Content) != "null" {
		if json.Unmarshal(wire.Content, &content) != nil {
			return nil, fmt.Errorf("%w for %q: content must be an object", ErrInvalidElicitationResult, f.key)
		}
	}
	if err := validateJSONSchema(f.schema, content); err != nil {
		return nil, fmt.Errorf("%w for %q: %v", ErrInvalidElicitationResult, f.key, err)
	}
	res.Content = content
	return res, nil
}

// ---- 2025-06-18 / 2025-11-25: elicitation/create on the request stream ----

// clientPeer is what the server knows about one Legacy client connection
// for server-to-client requests: the revision and capabilities its
// initialize declared, and the requests awaiting its response. stdio has
// one per connection; HTTP one per session (see Server.httpPeer).
type clientPeer struct {
	mu      sync.Mutex
	version string
	caps    map[string]any
	pending map[string]pendingAnswer // response id (compact JSON) -> waiter
	seen    time.Time
}

// pendingAnswer is a server-to-client request waiting for its response.
// principal is the authenticated caller the request went to (TokenInfo's
// Subject, "" without authentication): only a response from the same
// principal is accepted, so another caller who learns the id can't answer.
type pendingAnswer struct {
	ch        chan json.RawMessage
	principal string
}

func (p *clientPeer) record(version string, caps map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.version, p.caps, p.seen = version, caps, time.Now()
}

func (p *clientPeer) state() (version string, caps map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version, p.caps
}

func (p *clientPeer) await(id, principal string) chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]pendingAnswer{}
	}
	p.pending[id] = pendingAnswer{ch: ch, principal: principal}
	return ch
}

func (p *clientPeer) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pending, id)
}

// deliver hands a client's JSON-RPC response to the request waiting for it,
// when it comes from the principal the request went to. A response nobody
// waits for (late, after a timeout, or unknown) is dropped, and so is one
// from another principal, which leaves the request waiting for the real one.
func (p *clientPeer) deliver(id, principal string, msg json.RawMessage) bool {
	p.mu.Lock()
	w, ok := p.pending[id]
	if !ok || w.principal != principal {
		p.mu.Unlock()
		return false
	}
	delete(p.pending, id)
	p.mu.Unlock()
	w.ch <- msg
	return true
}

// principalOf is the authenticated caller of ctx (TokenInfo's Subject), or
// "" when the request carried no verified token.
func principalOf(ctx context.Context) string {
	if info := TokenInfoFromContext(ctx); info != nil {
		return info.Subject
	}
	return ""
}

type clientPeerKey struct{}

func withClientPeer(ctx context.Context, p *clientPeer) context.Context {
	return context.WithValue(ctx, clientPeerKey{}, p)
}

func clientPeerFrom(ctx context.Context) *clientPeer {
	p, _ := ctx.Value(clientPeerKey{}).(*clientPeer)
	return p
}

// httpPeerIdle is how long an HTTP session's peer entry is kept without use.
const httpPeerIdle = 2 * time.Hour

// httpPeerScope is the key an HTTP request's peer is filed under: its
// session id. Without session management there is none (""), and no peer:
// the server can't tell one Legacy HTTP client from another, so it can't
// know what each declared or whose answer a response is, and elicitation is
// not offered.
func (s *Server) httpPeerScope(r *http.Request) string {
	if s.getSessionManager() == nil {
		return ""
	}
	return r.Header.Get(headerSessionID)
}

// recordHTTPPeer remembers what an HTTP initialize declared, for elicitation
// on that session's later requests. Only sessions whose client declared
// elicitation are kept, and idle entries are swept here.
func (s *Server) recordHTTPPeer(scope, version string, caps map[string]any) {
	if scope == "" {
		return // no session: see httpPeerScope
	}
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	now := time.Now()
	for k, p := range s.peers {
		p.mu.Lock()
		idle := now.Sub(p.seen) > httpPeerIdle && len(p.pending) == 0
		p.mu.Unlock()
		if idle {
			delete(s.peers, k)
		}
	}
	if _, declared := caps["elicitation"]; !declared {
		delete(s.peers, scope)
		return
	}
	if s.peers == nil {
		s.peers = map[string]*clientPeer{}
	}
	p := s.peers[scope]
	if p == nil {
		p = &clientPeer{}
		s.peers[scope] = p
	}
	p.record(version, caps)
}

func (s *Server) httpPeer(scope string) *clientPeer {
	if scope == "" {
		return nil // no session: see httpPeerScope
	}
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	p := s.peers[scope]
	if p != nil {
		p.mu.Lock()
		p.seen = time.Now()
		p.mu.Unlock()
	}
	return p
}

func (s *Server) forgetHTTPPeer(scope string) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	delete(s.peers, scope)
}

// deliverHTTPResponse routes a client's JSON-RPC response POST (an id and no
// method) to the handler waiting for it in the same session, when it comes
// from the same authenticated principal.
func (s *Server) deliverHTTPResponse(r *http.Request, id any, raw []byte) {
	p := s.httpPeer(s.httpPeerScope(r))
	if p == nil {
		return
	}
	key, _ := json.Marshal(id)
	p.deliver(string(key), principalOf(r.Context()), raw)
}

// newServerRequestID returns an id for a server-to-client request. It is
// random (128 bits), not a counter: an answer is accepted by id, so an id
// must not be guessable by anyone but the client it was sent to.
func newServerRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("mcp: crypto/rand: " + err.Error())
	}
	return "elicitation-" + hex.EncodeToString(b[:])
}

type legacyElicitation struct {
	peer    *clientPeer
	rs      *requestStream
	version string
	timeout time.Duration
}

func (l *legacyElicitation) supported() bool {
	if l.peer == nil || l.rs == nil || l.rs.send == nil {
		return false
	}
	if l.version == "" || l.version < "2025-06-18" {
		return false // elicitation arrived in 2025-06-18
	}
	_, caps := l.peer.state()
	return formElicitationDeclared(caps)
}

func (l *legacyElicitation) elicit(ctx context.Context, forms []preparedForm) ([]*ElicitResult, error) {
	if !l.supported() {
		return nil, ErrElicitationUnsupported
	}
	out := make([]*ElicitResult, 0, len(forms))
	for _, f := range forms {
		res, err := l.one(ctx, f)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (l *legacyElicitation) one(ctx context.Context, f preparedForm) (*ElicitResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := newServerRequestID()
	key, _ := json.Marshal(id)
	ch := l.peer.await(string(key), principalOf(ctx))
	defer l.peer.forget(string(key))

	params := f.params(l.version >= "2025-11-25")
	if l.rs.requestID != nil {
		// Which of the client's requests this one serves. The spec gives a
		// stdio client no way to tell; a federating Client of this library
		// uses it to reach the right caller (client_elicitation.go).
		params["_meta"] = map[string]any{metaKeyRelatedRequest: l.rs.requestID}
	}
	switch err := l.rs.sendRequest(id, "elicitation/create", params); err {
	case nil:
	case errStreamUnavailable:
		return nil, ErrElicitationUnsupported
	default:
		return nil, err
	}

	var timeout <-chan time.Time
	if l.timeout > 0 {
		t := time.NewTimer(l.timeout)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case msg := <-ch:
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *MCPError       `json:"error"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidElicitationResult, err)
		}
		if resp.Error != nil {
			return nil, &ElicitationError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
		}
		return parseElicitResult(resp.Result, f)
	case <-ctx.Done():
		// The originating request was cancelled (its stream is finished, so
		// nothing is sent), or the handler's own deadline passed: tell the
		// client to stop asking.
		l.cancel(id, "request cancelled")
		return nil, ctx.Err()
	case <-timeout:
		l.cancel(id, "elicitation timed out")
		return nil, ErrElicitationTimeout
	}
}

func (l *legacyElicitation) cancel(id, reason string) {
	_ = l.rs.notify("notifications/cancelled", map[string]any{"requestId": id, "reason": reason})
}

// ---- 2026-07-28: InputRequiredResult (multi round-trip requests) ----

type mrtrElicitation struct {
	s         *Server
	method    string
	digest    string
	principal string
	caps      map[string]any
	carried   map[string]json.RawMessage // answers of earlier rounds, from verified requestState
	responses map[string]json.RawMessage // this retry's inputResponses
	// carriedSlots are federated calls' slots from verified requestState.
	carriedSlots map[string]json.RawMessage

	mu       sync.Mutex
	answered map[string]json.RawMessage
	pending  map[string]any
	failure  *MCPError
	stashed  map[string]json.RawMessage // slots written this run
	dropped  map[string]bool            // slots finished this run
}

func (m *mrtrElicitation) elicit(forms []preparedForm) ([]*ElicitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*ElicitResult, len(forms))
	var missing []preparedForm
	for i, f := range forms {
		raw, ok := m.carried[f.key]
		if !ok {
			raw, ok = m.responses[f.key]
		}
		if !ok {
			missing = append(missing, f)
			continue
		}
		res, err := parseElicitResult(raw, f)
		if err != nil {
			// A malformed answer is a protocol error (SHOULD validate
			// InputResponses and answer a JSON-RPC error).
			m.failure = &MCPError{Code: ErrorCodeInvalidParams, Message: "Invalid inputResponses", Data: map[string]any{"details": err.Error()}}
			return nil, err
		}
		if m.answered == nil {
			m.answered = map[string]json.RawMessage{}
		}
		m.answered[f.key] = raw
		out[i] = res
	}
	if len(missing) == 0 {
		return out, nil
	}
	// MUST NOT send an inputRequest the client has not declared support for.
	if !formElicitationDeclared(m.caps) {
		return nil, ErrElicitationUnsupported
	}
	if m.pending == nil {
		m.pending = map[string]any{}
	}
	for _, f := range missing {
		m.pending[f.key] = map[string]any{"method": "elicitation/create", "params": f.params(true)}
	}
	return nil, ErrElicitationPending
}

// requestStatePayload is what requestState carries, under its HMAC.
type requestStatePayload struct {
	V int                        `json:"v"`
	M string                     `json:"m"`           // method
	D string                     `json:"d"`           // digest of the salient params
	P string                     `json:"p,omitempty"` // authenticated principal
	E int64                      `json:"e"`           // expiry, Unix seconds
	R map[string]json.RawMessage `json:"r,omitempty"` // answers so far
	X map[string]json.RawMessage `json:"x,omitempty"` // federated calls' slots
}

const requestStateMACLabel = "mcp/requestState/v1."

func signRequestState(key []byte, p requestStatePayload) string {
	body, _ := json.Marshal(p)
	enc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(requestStateMACLabel + enc))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyRequestState(key []byte, token string) (requestStatePayload, error) {
	var p requestStatePayload
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return p, errors.New("requestState failed integrity verification")
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(requestStateMACLabel + enc))
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return p, errors.New("requestState failed integrity verification")
	}
	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || json.Unmarshal(body, &p) != nil || p.V != 1 {
		return p, errors.New("requestState failed integrity verification")
	}
	return p, nil
}

// salientDigest hashes a request's params minus the MRTR and _meta fields,
// identifying "the same request" across retries. Map keys marshal sorted,
// so the encoding is canonical.
func salientDigest(params map[string]any) string {
	trimmed := make(map[string]any, len(params))
	for k, v := range params {
		switch k {
		case "_meta", "inputResponses", "requestState":
			continue
		}
		trimmed[k] = v
	}
	raw, _ := json.Marshal(trimmed)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// newMRTR reads a 2026-07-28 request's inputResponses and requestState. A
// malformed inputResponses, or a requestState that fails verification, is
// an Invalid params error.
func (s *Server) newMRTR(ctx context.Context, method string, raw json.RawMessage) (*mrtrElicitation, *MCPError) {
	invalid := func(msg, details string) *MCPError {
		return &MCPError{Code: ErrorCodeInvalidParams, Message: msg, Data: map[string]any{"details": details}}
	}
	var params map[string]any
	if len(raw) > 0 && json.Unmarshal(raw, &params) != nil {
		return nil, invalid("Invalid params", "params must be an object")
	}
	var wire struct {
		InputResponses json.RawMessage `json:"inputResponses"`
		RequestState   json.RawMessage `json:"requestState"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &wire)
	}
	m := &mrtrElicitation{s: s, method: method, digest: salientDigest(params), caps: ClientCapabilitiesFromContext(ctx)}
	if info := TokenInfoFromContext(ctx); info != nil {
		m.principal = info.Subject
	}
	if _, present := params["inputResponses"]; present {
		if json.Unmarshal(wire.InputResponses, &m.responses) != nil || m.responses == nil {
			return nil, invalid("Invalid params", "inputResponses must be an object")
		}
	}
	if _, present := params["requestState"]; present {
		var token string
		if json.Unmarshal(wire.RequestState, &token) != nil {
			return nil, invalid("Invalid requestState", "requestState must be a string")
		}
		key, _ := s.requestStateConfig()
		state, err := verifyRequestState(key, token)
		switch {
		case err != nil:
			return nil, invalid("Invalid requestState", err.Error())
		case time.Now().Unix() > state.E:
			return nil, invalid("Invalid requestState", "requestState has expired")
		case state.M != m.method || state.D != m.digest:
			return nil, invalid("Invalid requestState", "requestState was issued for a different request")
		case state.P != m.principal:
			return nil, invalid("Invalid requestState", "requestState was issued to a different principal")
		}
		m.carried, m.carriedSlots = state.R, state.X
	}
	return m, nil
}

// inputRequired is the InputRequiredResult to answer with when the handler
// left questions pending, nil otherwise.
func (m *mrtrElicitation) inputRequired() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return nil
	}
	answers := make(map[string]json.RawMessage, len(m.carried)+len(m.answered))
	for k, v := range m.carried {
		answers[k] = v
	}
	for k, v := range m.answered {
		answers[k] = v
	}
	var slots map[string]json.RawMessage
	for k, v := range m.carriedSlots {
		if !m.dropped[k] {
			if slots == nil {
				slots = map[string]json.RawMessage{}
			}
			slots[k] = v
		}
	}
	for k, v := range m.stashed {
		if slots == nil {
			slots = map[string]json.RawMessage{}
		}
		slots[k] = v
	}
	key, ttl := m.s.requestStateConfig()
	state := signRequestState(key, requestStatePayload{
		V: 1, M: m.method, D: m.digest, P: m.principal,
		E: time.Now().Add(ttl).Unix(), R: answers, X: slots,
	})
	return map[string]any{
		"resultType":    "input_required",
		"inputRequests": m.pending,
		"requestState":  state,
	}
}

// ---- wiring into tools/call, prompts/get and resources/read ----

// beginElicitation prepares ctx for Elicit in a tools/call, prompts/get or
// resources/read handler. modern selects 2026-07-28 (raw are the request's
// params); otherwise version is the Legacy revision and peer the client the
// request came from (nil when unknown). The returned error is a protocol
// error to answer with instead of running the handler.
func (s *Server) beginElicitation(ctx context.Context, method string, raw json.RawMessage, modern bool, version string, peer *clientPeer) (context.Context, *elicitationRun, *MCPError) {
	run := &elicitationRun{}
	if modern {
		m, perr := s.newMRTR(ctx, method, raw)
		if perr != nil {
			return ctx, nil, perr
		}
		run.mrtr = m
	} else {
		run.legacy = &legacyElicitation{peer: peer, rs: requestStreamFrom(ctx), version: version, timeout: s.elicitationTimeout()}
	}
	return withElicitation(ctx, run), run, nil
}

// outcome reports what to answer instead of the handler's own result: a
// protocol error from a malformed answer, or an InputRequiredResult when
// questions are pending. Both nil means the handler's result stands.
func (run *elicitationRun) outcome() (inputRequired map[string]any, wireErr *MCPError) {
	if run == nil || run.mrtr == nil {
		return nil, nil
	}
	run.mrtr.mu.Lock()
	failure := run.mrtr.failure
	run.mrtr.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	return run.mrtr.inputRequired(), nil
}

// httpElicitation is beginElicitation for an HTTP request.
func (s *Server) httpElicitation(r *http.Request, req *MCPRequest) (context.Context, *elicitationRun, *MCPError) {
	if isModernRequest(r, req) {
		return s.beginElicitation(r.Context(), req.Method, requestParams(req), true, "", nil)
	}
	// legacyPreflight has checked the header names a supported revision;
	// absent, it means 2025-03-26, which has no elicitation.
	version, _ := negotiateProtocolVersion(r.Header.Get(headerProtocolVersion), "2025-03-26")
	return s.beginElicitation(r.Context(), req.Method, nil, false, version, s.httpPeer(s.httpPeerScope(r)))
}

// stdioElicitation is beginElicitation for a stdio request.
func (s *Server) stdioElicitation(ctx context.Context, method string, raw json.RawMessage) (context.Context, *elicitationRun, *MCPError) {
	if isStdioModern(ctx) {
		return s.beginElicitation(ctx, method, raw, true, "", nil)
	}
	peer := clientPeerFrom(ctx)
	version := ""
	if peer != nil {
		version, _ = peer.state()
	}
	return s.beginElicitation(ctx, method, raw, false, version, peer)
}
