package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Federated elicitation: a remote tool, prompt or resource that asks for
// input reaches the caller of this library's Server, and the answer goes back
// to the remote.
//
// When a Server forwards a call to a remote (RegisterRemoteServer, the
// namespace and unprefixed execute_tool fallbacks, NewRemoteProvider), the
// forwarding Client runs with the downstream handler's context. Whatever
// the remote asks is put to that caller with Elicit, so either downstream
// era works: a 2025-x caller gets elicitation/create and Elicit blocks; a
// 2026-07-28 caller gets an InputRequiredResult and the handler re-runs.
//
// Upstream 2025-06-18/2025-11-25 (client/elicitation): the remote sends
// elicitation/create while the forwarded call is in flight.
//
//   - The Client declares form-mode elicitation in initialize. The
//     declaration is per connection and the connection is shared by every
//     caller, so each request is answered for the forwarded call it belongs
//     to: on HTTP, the call whose response stream carries it; on stdio, the
//     call named by the request's io.github.paularlott/relatedRequestId
//     _meta hint (sent by this library's Server), or else the only call in
//     flight. When no caller can be found, or the caller cannot be asked
//     (ElicitationSupported is false), the answer is action "cancel": no
//     user made a choice. The remote is never left waiting.
//   - A 2026-07-28 caller cannot keep the remote's call open across its own
//     retry: the question goes into the caller's InputRequiredResult, the
//     forwarded call is cancelled (notifications/cancelled), and the retry
//     calls the remote again, answering its question from the caller's
//     inputResponses. The remote therefore repeats its work up to the
//     question, as any 2025-x server must allow a client to.
//
// Upstream 2026-07-28 (basic/patterns/mrtr): the remote answers with an
// InputRequiredResult.
//
//   - Each request carries the remote only the capabilities the caller can
//     honour: elicitation when the caller can be asked, never sampling or
//     roots.
//   - Every elicitation/create input request is put to the caller
//     (ElicitAll); the call is retried with inputResponses and the remote's
//     exact requestState, with a new request id, until the remote completes.
//   - When the caller is on 2026-07-28 too, its handler re-runs on each of
//     its retries. The remote's pending input requests and requestState are
//     kept in this server's own signed requestState (a slot per federated
//     call), so the re-run resumes the remote's exchange rather than calling
//     the remote afresh.
//   - Sampling and roots input requests are not relayed (deprecated by
//     2026-07-28, not implemented): the call fails with ErrInputRequired.

// Answering elicitation in the application.
//
// OnElicitation lets an application answer a server's form-mode questions
// itself (a CLI prompt, a UI dialog). Both eras use it: a 2025-x server's
// elicitation/create request, and the elicitation input requests of a
// 2026-07-28 InputRequiredResult, which the Client then retries with the
// answers and the server's exact requestState. When the Client is
// forwarding a call for a Server whose own caller can be asked (federation,
// above), the question goes to that caller instead.

// ElicitationRequest is a server's form-mode question, as an
// ElicitationHandler receives it.
type ElicitationRequest struct {
	// Message explains what the server is asking and why.
	Message string
	// Schema is the server's requestedSchema: a flat object of primitive
	// properties, which may carry defaults (see OnElicitation).
	Schema map[string]any
}

// ElicitationHandler answers a server's elicitation: ElicitAccept with the
// user's values, ElicitDecline, or ElicitCancel. An error is reported to the
// server as a cancelled elicitation (no user made a choice).
type ElicitationHandler func(ctx context.Context, req ElicitationRequest) (*ElicitResult, error)

// OnElicitation registers fn to answer the servers' form-mode elicitation
// requests, and makes the Client declare form-mode elicitation. On
// ElicitAccept, any schema property fn leaves out that has a default is
// filled with it, as the spec asks clients that support defaults to do
// (SEP-1034). Pass nil to stop answering: questions are then cancelled, and
// a 2026-07-28 request that needs an answer fails with ErrInputRequired.
//
// Against a 2025-x server over HTTP the Client also opens the session's GET
// event stream (as EnableNotifications does), because the server may send
// its questions there rather than on the call's response stream; release it
// with Close. Register the handler before Initialize so initialize declares
// it. Returns the client for chaining.
func (c *Client) OnElicitation(fn ElicitationHandler) *Client {
	c.elicitMu.Lock()
	c.elicitHandler = fn
	c.elicitMu.Unlock()
	if fn != nil {
		c.mu.RLock()
		ready := c.initialized && c.transport == nil && c.era != eraModern
		era := c.era
		c.mu.RUnlock()
		if ready {
			c.startNotifications(era)
		}
	}
	return c
}

func (c *Client) elicitationHandler() ElicitationHandler {
	c.elicitMu.Lock()
	defer c.elicitMu.Unlock()
	return c.elicitHandler
}

// askApplication puts one question to the application's handler, applying
// the schema's defaults to an accepted answer. Any failure is a cancel.
func askApplication(ctx context.Context, fn ElicitationHandler, message string, schema map[string]any) *ElicitResult {
	res, err := fn(ctx, ElicitationRequest{Message: message, Schema: schema})
	if err != nil || res == nil {
		return &ElicitResult{Action: ElicitCancel}
	}
	switch res.Action {
	case ElicitAccept:
		return &ElicitResult{Action: ElicitAccept, Content: withSchemaDefaults(schema, res.Content)}
	case ElicitDecline, ElicitCancel:
		return &ElicitResult{Action: res.Action}
	}
	return &ElicitResult{Action: ElicitCancel}
}

// withSchemaDefaults returns content with every schema property that has a
// default and isn't in content set to that default. content is not modified.
func withSchemaDefaults(schema, content map[string]any) map[string]any {
	out := make(map[string]any, len(content))
	for k, v := range content {
		out[k] = v
	}
	props, _ := schema["properties"].(map[string]any)
	for name, raw := range props {
		p, _ := raw.(map[string]any)
		if def, ok := p["default"]; ok {
			if _, set := out[name]; !set {
				out[name] = def
			}
		}
	}
	return out
}

// metaKeyRelatedRequest is the _meta key on this library's elicitation/create
// requests naming the client request they serve.
const metaKeyRelatedRequest = "io.github.paularlott/relatedRequestId"

// maxInputRounds bounds the InputRequiredResult rounds of one forwarded call.
const maxInputRounds = 16

// elicitRelay is one forwarded call that a remote's elicitation/create may
// belong to.
type elicitRelay struct {
	ctx    context.Context // the forwarded call's context: the downstream handler's
	slot   string
	canAsk bool
	cancel context.CancelFunc

	mu    sync.Mutex
	seq   int
	abort error // set when the call must stop with this error (ErrElicitationPending)
}

func (r *elicitRelay) aborted() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.abort
}

func relayKey(id any) string {
	raw, _ := json.Marshal(normalizeJSONRPCID(id))
	return string(raw)
}

func (c *Client) trackRelay(id any, r *elicitRelay) func() {
	key := relayKey(id)
	c.elicitMu.Lock()
	if c.elicitRelays == nil {
		c.elicitRelays = map[string]*elicitRelay{}
	}
	c.elicitRelays[key] = r
	c.elicitMu.Unlock()
	return func() {
		c.elicitMu.Lock()
		delete(c.elicitRelays, key)
		c.elicitMu.Unlock()
	}
}

// relayFor finds the forwarded call a server-to-client request belongs to:
// the one with id when known, else the only call in flight.
func (c *Client) relayFor(id any, known bool) *elicitRelay {
	c.elicitMu.Lock()
	defer c.elicitMu.Unlock()
	if known {
		return c.elicitRelays[relayKey(id)]
	}
	if len(c.elicitRelays) == 1 {
		for _, r := range c.elicitRelays {
			return r
		}
	}
	return nil
}

// answerElicitation answers a remote's elicitation/create for relay (nil
// when no forwarded call could be identified) by asking relay's caller.
// When the caller is on 2026-07-28 and has not answered yet, relay.abort is
// set: the caller of answerElicitation sends the reply, then cancels the call.
func (c *Client) answerElicitation(relay *elicitRelay, raw json.RawMessage) (any, *MCPError) {
	cancelled := map[string]any{"action": string(ElicitCancel)}
	handler := c.elicitationHandler()
	if (relay == nil || !relay.canAsk) && handler == nil {
		return cancelled, nil
	}
	var p struct {
		Mode            string         `json:"mode"`
		Message         string         `json:"message"`
		RequestedSchema map[string]any `json:"requestedSchema"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &MCPError{Code: ErrorCodeInvalidParams, Message: "Invalid params", Data: map[string]any{"details": err.Error()}}
	}
	if p.Mode != "" && p.Mode != "form" {
		return nil, &MCPError{Code: ErrorCodeInvalidParams, Message: "Unsupported elicitation mode", Data: map[string]any{"details": "this client declared form mode only"}}
	}
	if relay == nil || !relay.canAsk {
		// No downstream caller to relay to: the application answers.
		ctx := context.Background()
		if relay != nil {
			ctx = relay.ctx
		}
		return elicitResultWire(askApplication(ctx, handler, p.Message, p.RequestedSchema)), nil
	}
	relay.mu.Lock()
	relay.seq++
	key := fmt.Sprintf("%s.legacy.%d", relay.slot, relay.seq)
	relay.mu.Unlock()

	res, err := Elicit(relay.ctx, ElicitForm{Key: key, Message: p.Message, Schema: p.RequestedSchema})
	var clientErr *ElicitationError
	switch {
	case err == nil:
		return elicitResultWire(res), nil
	case errors.Is(err, ErrElicitationPending):
		relay.mu.Lock()
		relay.abort = err
		relay.mu.Unlock()
		return cancelled, nil
	case errors.As(err, &clientErr):
		return nil, &MCPError{Code: clientErr.Code, Message: clientErr.Message, Data: clientErr.Data}
	case errors.Is(err, ErrElicitationUnsupported), errors.Is(err, ErrElicitationTimeout),
		errors.Is(err, ErrInvalidElicitationResult), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return cancelled, nil
	default: // the request itself is unusable (a schema outside the spec's subset)
		return nil, &MCPError{Code: ErrorCodeInvalidParams, Message: "Invalid params", Data: map[string]any{"details": err.Error()}}
	}
}

func elicitResultWire(res *ElicitResult) map[string]any {
	out := map[string]any{"action": string(res.Action)}
	if res.Action == ElicitAccept {
		out["content"] = res.Content
	}
	return out
}

// relayedInput is a 2026-07-28 remote's open InputRequiredResult, as kept in
// the caller's requestState between the caller's rounds.
type relayedInput struct {
	Method   string         `json:"m"`
	Digest   string         `json:"d"`
	Round    int            `json:"n"`
	Requests map[string]any `json:"q"`
	State    *string        `json:"s,omitempty"`
}

// sendWithInput is sendRequest for tools/call, resources/read and
// prompts/get, the requests that may need the user's input: it relays the
// remote's elicitation to the caller of ctx in either era (see the top of
// this file) and returns the remote's final response in resp.
func (c *Client) sendWithInput(ctx context.Context, req *MCPRequest, resp *MCPResponse, respHeaders *http.Header) error {
	run := elicitationFrom(ctx)
	slot := "relay"
	if run != nil {
		slot = run.nextRelaySlot()
	}
	params, _ := req.Params.(map[string]any)
	digest := salientDigest(params)
	canAsk := ElicitationSupported(ctx)

	cur := *req
	cur.declareElicitation = canAsk || c.elicitationHandler() != nil
	var open *relayedInput
	if raw, ok := run.recall(slot); ok {
		var saved relayedInput
		if json.Unmarshal(raw, &saved) == nil && saved.Method == req.Method && saved.Digest == digest {
			open = &saved // resume the remote's exchange rather than call it afresh
		}
	}

	if open == nil {
		callCtx, cancel := context.WithCancel(ctx)
		relay := &elicitRelay{ctx: ctx, slot: slot, canAsk: canAsk, cancel: cancel}
		untrack := c.trackRelay(cur.ID, relay)
		err := c.sendRequest(callCtx, &cur, resp, respHeaders)
		untrack()
		cancel()
		if abort := relay.aborted(); abort != nil {
			return abort // the caller is asked; its retry calls the remote again
		}
		if err != nil {
			return err
		}
		requests, state, isInput := c.inputRequiredOf(resp)
		if !isInput {
			run.drop(slot)
			return nil
		}
		open = &relayedInput{Method: req.Method, Digest: digest, Requests: requests, State: state}
	}

	for {
		if open.Round >= maxInputRounds {
			return fmt.Errorf("%w: the remote server asked for input %d times", ErrInputRequired, open.Round)
		}
		responses, err := c.answerInputRequests(ctx, slot, open)
		if errors.Is(err, ErrElicitationPending) {
			saved, _ := json.Marshal(open)
			run.remember(slot, saved)
			return err
		}
		if err != nil {
			return err
		}

		// Retry: same request, a new id (MUST differ), the answers and the
		// remote's requestState echoed exactly (MUST, and none when the
		// remote sent none).
		next := make(map[string]any, len(params)+2)
		for k, v := range params {
			next[k] = v
		}
		next["inputResponses"] = responses
		if open.State != nil {
			next["requestState"] = *open.State
		}
		cur = MCPRequest{JSONRPC: "2.0", ID: c.newRequestID(), Method: req.Method, Params: next, declareElicitation: cur.declareElicitation}
		*resp = MCPResponse{}
		if err := c.sendRequest(ctx, &cur, resp, respHeaders); err != nil {
			return err
		}
		requests, state, isInput := c.inputRequiredOf(resp)
		if !isInput {
			run.drop(slot)
			return nil
		}
		open = &relayedInput{Method: req.Method, Digest: digest, Round: open.Round + 1, Requests: requests, State: state}
	}
}

// inputRequiredOf reports whether resp is a 2026-07-28 InputRequiredResult,
// with its inputRequests and requestState (nil when the remote sent none).
func (c *Client) inputRequiredOf(resp *MCPResponse) (map[string]any, *string, bool) {
	if c.era != eraModern || resp.Error != nil {
		return nil, nil, false
	}
	result, _ := resp.Result.(map[string]any)
	if result["resultType"] != "input_required" {
		return nil, nil, false
	}
	requests, _ := result["inputRequests"].(map[string]any)
	var state *string
	if s, ok := result["requestState"].(string); ok {
		state = &s
	}
	return requests, state, true
}

// answerInputRequests puts a remote's elicitation input requests to the
// caller of ctx and returns the inputResponses for the retry.
func (c *Client) answerInputRequests(ctx context.Context, slot string, open *relayedInput) (map[string]any, error) {
	keys := make([]string, 0, len(open.Requests))
	for k := range open.Requests {
		keys = append(keys, k)
	}
	sort.Strings(keys) // ElicitForm keys must be the same on every re-run
	forms := make([]ElicitForm, 0, len(keys))
	for _, k := range keys {
		r, _ := open.Requests[k].(map[string]any)
		method, _ := r["method"].(string)
		if method != "elicitation/create" {
			return nil, fmt.Errorf("%w: the remote server asks for %s input, which this library does not implement", ErrInputRequired, method)
		}
		p, _ := r["params"].(map[string]any)
		if mode, _ := p["mode"].(string); mode != "" && mode != "form" {
			return nil, fmt.Errorf("%w: the remote server asks for %s-mode elicitation; only form mode is supported", ErrInputRequired, mode)
		}
		message, _ := p["message"].(string)
		schema, _ := p["requestedSchema"].(map[string]any)
		forms = append(forms, ElicitForm{Key: fmt.Sprintf("%s.%d.%s", slot, open.Round, k), Message: message, Schema: schema})
	}
	if len(forms) == 0 {
		return map[string]any{}, nil // only requestState: retry at once (MAY)
	}
	if handler := c.elicitationHandler(); handler != nil && !ElicitationSupported(ctx) {
		// No downstream caller to relay to: the application answers.
		out := make(map[string]any, len(keys))
		for i, k := range keys {
			out[k] = elicitResultWire(askApplication(ctx, handler, forms[i].Message, forms[i].Schema))
		}
		return out, nil
	}
	results, err := ElicitAll(ctx, forms...)
	if errors.Is(err, ErrElicitationUnsupported) {
		return nil, fmt.Errorf("%w: the remote server asks for elicitation and the caller cannot be asked", ErrInputRequired)
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(keys))
	for i, k := range keys {
		out[k] = elicitResultWire(results[i])
	}
	return out, nil
}

// legacyElicitationCapabilities adds form-mode elicitation to the
// capabilities a Legacy initialize declares (an empty object: form only).
func legacyElicitationCapabilities(caps map[string]any) map[string]any {
	caps["elicitation"] = map[string]any{}
	return caps
}

// relatedRequestOf reads this library's related-request hint from a
// server-to-client request's params.
func relatedRequestOf(raw json.RawMessage) (any, bool) {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil, false
	}
	id, ok := p.Meta[metaKeyRelatedRequest]
	if !ok {
		return nil, false
	}
	return normalizeJSONRPCRawID(id), true
}
