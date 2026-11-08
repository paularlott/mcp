package mcp

import (
	"context"
	"fmt"
)

// NotificationProgress is the progress notification every protocol revision
// defines (2025-11-25 utilities/progress; 2026-07-28 patterns/progress).
const NotificationProgress = "notifications/progress"

// Relaying a remote tool's progress.
//
// When a Server federates a remote tool (RegisterRemoteServer) and its own
// caller asked for progress, the remote call asks for progress too, under a
// token of this client's own, and every notifications/progress the remote
// sends for it is reported on the caller's request with ReportProgress. The
// caller's token cannot simply be passed on: tokens MUST be unique across
// the receiver's active requests, and the upstream server is shared by
// every downstream caller. ReportProgress applies the spec's rules to the
// relayed values like any other (increasing, nothing after completion, rate
// limited).

// relayProgress adds a progressToken to params (a tools/call's params) when
// ctx belongs to a server request that asked for progress, and registers the
// relay for it. The returned func removes the relay; call it when the call
// returns, after which late notifications for the token are dropped.
func (c *Client) relayProgress(ctx context.Context, params map[string]any) func() {
	if _, ok := ProgressToken(ctx); !ok {
		return func() {}
	}
	token := fmt.Sprintf("mcp-relay-%d", c.nextProgress.Add(1))
	params["_meta"] = map[string]any{"progressToken": token}
	c.progressMu.Lock()
	if c.progressRelays == nil {
		c.progressRelays = map[string]func(progress, total float64, message string){}
	}
	c.progressRelays[token] = func(progress, total float64, message string) {
		_ = ReportProgress(ctx, progress, total, message)
	}
	c.progressMu.Unlock()
	return func() {
		c.progressMu.Lock()
		delete(c.progressRelays, token)
		c.progressMu.Unlock()
	}
}

// deliverProgress hands a notifications/progress to the relay registered for
// its token, if any. Transport readers call it as the notification arrives
// (the HTTP response stream, the stdio peer); it never blocks, since
// ReportProgress only queues.
func (c *Client) deliverProgress(params any) {
	p, _ := params.(map[string]any)
	token, _ := p["progressToken"].(string)
	progress, ok := p["progress"].(float64)
	if token == "" || !ok {
		return
	}
	total, _ := p["total"].(float64)
	message, _ := p["message"].(string)
	c.progressMu.Lock()
	relay := c.progressRelays[token]
	c.progressMu.Unlock()
	if relay != nil {
		relay(progress, total, message)
	}
}
