package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OnToolsChanged registers a callback fired whenever a
// notifications/tools/listChanged arrives from the server (HTTP SSE or stdio).
// The client also invalidates its own tool cache automatically; use this to
// trigger application-level refreshes (e.g. re-pulling for a UI). Returns the
// client for chaining.
func (c *Client) OnToolsChanged(fn func()) *Client {
	c.mu.Lock()
	c.onToolsChanged = fn
	c.mu.Unlock()
	return c
}

// OnResourcesChanged registers a callback for notifications/resources/listChanged.
func (c *Client) OnResourcesChanged(fn func()) *Client {
	c.mu.Lock()
	c.onResourcesChanged = fn
	c.mu.Unlock()
	return c
}

// OnPromptsChanged registers a callback for notifications/prompts/listChanged.
func (c *Client) OnPromptsChanged(fn func()) *Client {
	c.mu.Lock()
	c.onPromptsChanged = fn
	c.mu.Unlock()
	return c
}

// EnableNotifications opts the HTTP client into receiving server-pushed
// notifications over a long-lived SSE stream. When enabled, the client opens a
// background GET event-stream connection after Initialize, invalidates its tool
// cache on notifications/tools/listChanged, and fires any On*Changed callbacks.
//
// Notifications are off by default: a long-lived connection is only wanted when
// the caller intends to consume change events, and it must be released with
// [Client.Close]. stdio clients always receive notifications (no opt-in needed)
// because their transport is already a persistent connection.
//
// Returns the client for chaining. Safe to call before or after Initialize.
func (c *Client) EnableNotifications() *Client {
	c.readerMu.Lock()
	c.wantNotifications = true
	c.readerMu.Unlock()
	// If already initialized over HTTP, start the reader now; otherwise Initialize
	// will start it when it completes.
	c.mu.RLock()
	ready := c.initialized && c.transport == nil
	c.mu.RUnlock()
	if ready {
		c.startNotifications()
	}
	return c
}

// startNotifications launches the background SSE reader for an HTTP client,
// once. It is a no-op for stream transports (stdio), which receive
// notifications via their peer handlers and call handleNotification directly.
func (c *Client) startNotifications() {
	if c.transport != nil {
		return
	}
	c.readerMu.Lock()
	defer c.readerMu.Unlock()
	if c.readerStarted {
		return
	}
	c.readerStarted = true
	c.ctx, c.cancel = context.WithCancel(context.Background())
	ctx := c.ctx
	c.readerWG.Add(1)
	go func() {
		defer c.readerWG.Done()
		c.runSSEReader(ctx)
	}()
}

// runSSEReader maintains a long-lived GET event-stream connection, reconnecting
// with exponential backoff after transient failures until the client is closed.
func (c *Client) runSSEReader(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.connectAndReadSSE(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = time.Second
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// connectAndReadSSE opens one GET event-stream request and blocks reading
// notifications until the stream ends or the context is cancelled.
func (c *Client) connectAndReadSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Protocol-Version", MCPProtocolVersionLatest)

	c.mu.RLock()
	sessionID := c.sessionID
	auth := c.auth
	c.mu.RUnlock()
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if auth != nil {
		if h, err := auth.GetAuthHeader(); err == nil && h != "" {
			req.Header.Set("Authorization", h)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream returned status %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	for {
		if ctx.Err() != nil {
			return nil
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == ':' { // blank or comment/heartbeat
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 {
			continue
		}
		var msg struct {
			Method string `json:"method"`
			Params any    `json:"params"`
		}
		if json.Unmarshal(payload, &msg) != nil {
			continue
		}
		c.handleNotification(msg.Method, msg.Params)
	}
}

// handleNotification processes one inbound notification: invalidates the
// relevant cache, fires the user callback, then the internal propagation hook.
// Shared by the HTTP SSE reader and the stdio peer handlers.
func (c *Client) handleNotification(method string, params any) {
	switch method {
	case NotificationToolsChanged:
		c.mu.Lock()
		c.cachedTools = nil
		cb := c.onToolsChanged
		c.mu.Unlock()
		if cb != nil {
			cb()
		}
	case NotificationResourcesChanged:
		c.mu.RLock()
		cb := c.onResourcesChanged
		c.mu.RUnlock()
		if cb != nil {
			cb()
		}
	case NotificationPromptsChanged:
		c.mu.RLock()
		cb := c.onPromptsChanged
		c.mu.RUnlock()
		if cb != nil {
			cb()
		}
	}

	c.mu.RLock()
	hook := c.onNotification
	c.mu.RUnlock()
	if hook != nil {
		hook(method, params)
	}
}

// setPropagationHook installs an internal callback fired for every inbound
// notification (after cache handling). It is used by [Server.RegisterRemoteServer]
// to propagate upstream listChanged notifications downstream. Package-private.
func (c *Client) setPropagationHook(fn func(method string, params any)) {
	c.mu.Lock()
	c.onNotification = fn
	c.mu.Unlock()
}
