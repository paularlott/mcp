package mcp

import (
	"context"
	"net/http"
)

// Client side of x-mcp-header (see xmcpheader.go): a Modern-era client on
// Streamable HTTP mirrors each annotated tool argument into an
// Mcp-Param-<Name> header on tools/call. The annotations come from the
// tool's inputSchema as listed by tools/list (fetched on first use if the
// tool list has not been loaded yet).

type toolCallHeadersKey struct{}

// toolCallHeadersFrom returns the Mcp-Param-* headers CallTool attached to
// ctx for sendModernHTTPRequest, if any.
func toolCallHeadersFrom(ctx context.Context) http.Header {
	h, _ := ctx.Value(toolCallHeadersKey{}).(http.Header)
	return h
}

// mirrorsParamHeaders reports whether this client must mirror x-mcp-header
// arguments: Modern era over Streamable HTTP (other transports MAY ignore
// the annotations, and the Legacy era predates them).
func (c *Client) mirrorsParamHeaders() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.era == eraModern && c.transport == nil
}

// withToolCallHeaders returns ctx carrying the Mcp-Param-* headers for a
// tools/call of name (as listed, i.e. namespaced) with args.
//
// lookupErr is non-fatal: tools/list could not be loaded to learn the
// annotations, so the call goes out without Mcp-Param-* headers. It is
// returned so that, if the server then answers HeaderMismatch, the caller can
// report the real cause. err is fatal: an argument whose value cannot be
// represented as its annotated type (the server would reject the call).
func (c *Client) withToolCallHeaders(ctx context.Context, name string, args map[string]any) (out context.Context, lookupErr, err error) {
	if !c.mirrorsParamHeaders() {
		return ctx, nil, nil
	}
	schema, ok := c.cachedToolSchema(name)
	if !ok {
		if _, listErr := c.ListTools(ctx); listErr != nil {
			return ctx, listErr, nil
		}
		if schema, ok = c.cachedToolSchema(name); !ok {
			return ctx, nil, nil
		}
	}
	bindings, bindErr := schemaHeaderBindings(schema)
	if bindErr != nil || len(bindings) == 0 {
		return ctx, nil, nil
	}
	headers, err := mcpParamHeaders(bindings, args)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, toolCallHeadersKey{}, headers), nil, nil
}

// cachedToolSchema looks up a tool's inputSchema in the tools/list cache.
func (c *Client) cachedToolSchema(name string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cachedTools == nil {
		return nil, false
	}
	for _, tool := range c.cachedTools {
		if tool.Name == name {
			return tool.InputSchema, true
		}
	}
	return nil, true // list loaded; tool not in it (filtered, or unknown)
}
