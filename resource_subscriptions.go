package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/paularlott/jsonrpc"
)

// NotificationResourceUpdated is the notification a server sends to a client
// subscribed to a resource when that resource changes, in every protocol
// revision: after resources/subscribe (2025-03-26 through 2025-11-25), or on a
// subscriptions/listen stream that listed the URI in
// notifications.resourceSubscriptions (2026-07-28).
const NotificationResourceUpdated = "notifications/resources/updated"

// maxResourceSubscriptions bounds the resource URIs one subscriber (a Legacy
// session or stdio connection, or one Modern listen stream) may watch, so a
// client cannot grow the server's subscription state without limit.
const maxResourceSubscriptions = 1024

// resourceSubscriptionIdleTTL is how long a Legacy HTTP session's resource
// subscriptions outlive its last sign of life (a subscribe, an unsubscribe,
// or its GET stream opening or closing) while it has no GET stream open.
// Sessions are validated by a pluggable SessionManager that does not report
// expiry, so subscriptions of a session that silently expired are pruned on
// this timer instead.
const resourceSubscriptionIdleTTL = time.Hour

// resourceSubscriptionSink is a notificationSink that keeps its own set of
// subscribed resource URIs: a Legacy stdio connection (resources/subscribe on
// that connection) or a Modern subscriptions/listen stream (its
// resourceSubscriptions filter). resourceUpdated MUST be non-blocking, and
// MUST NOT deliver an update for a URI the subscriber did not ask for.
type resourceSubscriptionSink interface {
	notificationSink
	resourceUpdated(uri string)
}

// resourceSet is a bounded, concurrency-safe set of resource URIs.
type resourceSet struct {
	mu   sync.RWMutex
	uris map[string]struct{}
}

// add reports false when the set is full and uri is not already in it.
func (rs *resourceSet) add(uri string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.uris == nil {
		rs.uris = map[string]struct{}{}
	}
	if _, ok := rs.uris[uri]; !ok && len(rs.uris) >= maxResourceSubscriptions {
		return false
	}
	rs.uris[uri] = struct{}{}
	return true
}

func (rs *resourceSet) remove(uri string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.uris, uri)
}

func (rs *resourceSet) has(uri string) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	_, ok := rs.uris[uri]
	return ok
}

// sessionResources is one Legacy HTTP session's resource subscriptions.
type sessionResources struct {
	uris     map[string]struct{}
	lastSeen time.Time
}

// subscribeSessionResource records a Legacy HTTP session's subscription to
// uri. Subscriptions belong to the session (its MCP-Session-Id), not to one
// GET stream, so they survive the client reconnecting that stream. It reports
// false when the session already watches maxResourceSubscriptions URIs.
func (h *notificationHub) subscribeSessionResource(sessionID, uri string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.pruneSessionResourcesLocked(now)
	sr := h.sessionResources[sessionID]
	if sr == nil {
		sr = &sessionResources{uris: map[string]struct{}{}}
		h.sessionResources[sessionID] = sr
	}
	sr.lastSeen = now
	if _, ok := sr.uris[uri]; !ok && len(sr.uris) >= maxResourceSubscriptions {
		return false
	}
	sr.uris[uri] = struct{}{}
	return true
}

// unsubscribeSessionResource drops a Legacy HTTP session's subscription to
// uri; unsubscribing from a URI the session never subscribed to is a no-op.
func (h *notificationHub) unsubscribeSessionResource(sessionID, uri string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sr := h.sessionResources[sessionID]; sr != nil {
		delete(sr.uris, uri)
		sr.lastSeen = time.Now()
		if len(sr.uris) == 0 {
			delete(h.sessionResources, sessionID)
		}
	}
}

// dropSessionResources forgets every subscription of a terminated session.
func (h *notificationHub) dropSessionResources(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessionResources, sessionID)
}

// touchSessionResourcesLocked marks a session as alive (its GET stream opened
// or closed), deferring the idle prune of its subscriptions.
func (h *notificationHub) touchSessionResourcesLocked(sessionID string) {
	if sr := h.sessionResources[sessionID]; sr != nil {
		sr.lastSeen = time.Now()
	}
}

// pruneSessionResourcesLocked drops the subscriptions of sessions with no GET
// stream open that have been idle for resourceSubscriptionIdleTTL, at most
// once a minute.
func (h *notificationHub) pruneSessionResourcesLocked(now time.Time) {
	if now.Sub(h.lastResourcePrune) < time.Minute {
		return
	}
	h.lastResourcePrune = now
	for sessionID, sr := range h.sessionResources {
		if _, streaming := h.bySession[sessionID]; streaming {
			continue
		}
		if now.Sub(sr.lastSeen) > resourceSubscriptionIdleTTL {
			delete(h.sessionResources, sessionID)
		}
	}
}

// resourceUpdated delivers notifications/resources/updated for uri to exactly
// the subscribers that asked for it: the GET stream of each Legacy HTTP
// session subscribed to uri, and every stdio connection or Modern listen
// stream whose own subscription set holds it. Nothing else receives it — the
// spec only has the server send this notification to subscribed clients.
// A subscribed Legacy session with no GET stream open misses the update, as
// the Streamable HTTP transport has nowhere to put it.
func (h *notificationHub) resourceUpdated(uri string) {
	params := map[string]any{"uri": uri}
	h.mu.RLock()
	var sessionSinks []notificationSink
	for sessionID, sr := range h.sessionResources {
		if _, ok := sr.uris[uri]; !ok {
			continue
		}
		if id, ok := h.bySession[sessionID]; ok {
			if sub := h.subscribers[id]; sub != nil {
				sessionSinks = append(sessionSinks, sub.sink)
			}
		}
	}
	var own []resourceSubscriptionSink
	for _, sub := range h.subscribers {
		if rs, ok := sub.sink.(resourceSubscriptionSink); ok {
			own = append(own, rs)
		}
	}
	h.mu.RUnlock()
	for _, sink := range sessionSinks {
		sink.send(NotificationResourceUpdated, params)
	}
	for _, sink := range own {
		sink.resourceUpdated(uri)
	}
}

// NotifyResourceUpdated emits notifications/resources/updated for uri to
// every client subscribed to it, on every protocol revision this server
// serves: Legacy HTTP sessions that sent resources/subscribe (on the
// session's GET stream), Legacy stdio connections that did, and Modern
// subscriptions/listen streams that listed uri in resourceSubscriptions
// (tagged with that stream's subscriptionId). Clients that did not subscribe
// to uri receive nothing.
//
// The server cannot see a resource's content change, so call this whenever
// it does — for example from a file watcher or after a write. Matching is by
// exact URI; the spec allows the notified URI to be a sub-resource of the
// one subscribed to, so a server that wants that calls this once per URI
// clients may have subscribed to.
func (s *Server) NotifyResourceUpdated(uri string) {
	if uri == "" {
		return
	}
	s.notifications.resourceUpdated(uri)
}

// resourceSubscribeParams is the params object of resources/subscribe and
// resources/unsubscribe.
type resourceSubscribeParams struct {
	URI string `json:"uri"`
}

// handleResourcesSubscribe serves Legacy resources/subscribe and
// resources/unsubscribe over HTTP. Subscriptions belong to the session, so
// they need session management (SetSessionManager): without it the server
// cannot tell which GET stream belongs to the subscribing client, so it
// declares resources.subscribe false and answers both methods as unknown
// (-32601) rather than accept a subscription it could only honour by
// broadcasting. Both return an empty result on success.
func (s *Server) handleResourcesSubscribe(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	sessionID := r.Header.Get(headerSessionID)
	if s.getSessionManager() == nil || sessionID == "" {
		s.sendMCPError(w, req.ID, ErrorCodeMethodNotFound, "Method not found", map[string]any{"method": req.Method})
		return
	}
	var params resourceSubscribeParams
	if err := s.parseParams(req, &params); err != nil {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Invalid params", map[string]any{"details": err.Error()})
		return
	}
	if params.URI == "" {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "uri parameter is required", nil)
		return
	}
	if req.Method == "resources/unsubscribe" {
		s.notifications.unsubscribeSessionResource(sessionID, params.URI)
	} else if !s.notifications.subscribeSessionResource(sessionID, params.URI) {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Too many resource subscriptions", map[string]any{"uri": params.URI, "limit": maxResourceSubscriptions})
		return
	}
	s.sendMCPResponse(w, req.ID, map[string]any{})
}

// stdioResourcesSubscribe serves Legacy resources/subscribe and
// resources/unsubscribe over stdio, where the connection is the session: the
// subscription is recorded on this connection's notification sink, which
// writes updates for it once the client has completed a Legacy initialize.
func stdioResourcesSubscribe(sink *streamNotifySink, unsubscribe bool) func(ctx context.Context, params json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var params resourceSubscribeParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Invalid params", map[string]any{"details": err.Error()})
			}
		}
		if params.URI == "" {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "uri parameter is required", nil)
		}
		if unsubscribe {
			sink.resources.remove(params.URI)
		} else if !sink.resources.add(params.URI) {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Too many resource subscriptions", map[string]any{"uri": params.URI, "limit": maxResourceSubscriptions})
		}
		return map[string]any{}, nil
	}
}

// modernNotificationParams builds the params of a notification delivered on
// a Modern subscription: the event's own fields (uri, for
// notifications/resources/updated) plus _meta carrying the subscriptionId,
// which every message on the stream must have.
func modernNotificationParams(meta map[string]any, eventParams any) map[string]any {
	params := map[string]any{"_meta": meta}
	if m, ok := eventParams.(map[string]any); ok {
		for k, v := range m {
			if k != "_meta" {
				params[k] = v
			}
		}
	}
	return params
}
