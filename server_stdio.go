package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/paularlott/jsonrpc"
)

// StdioOption configures a stdio MCP server run.
type StdioOption func(*stdioConfig)

type stdioConfig struct {
	showAll bool
}

// WithStdioShowAllTools makes the stdio server expose every tool (including
// discoverable ones) on tools/list, bypassing the discovery meta-tools. It is
// the stdio equivalent of the HTTP show-all header/query flag.
func WithStdioShowAllTools() StdioOption {
	return func(c *stdioConfig) { c.showAll = true }
}

// ServeStdio serves the MCP protocol over newline-delimited JSON-RPC 2.0 on
// os.Stdin/os.Stdout. It is the entry point for an MCP stdio server (the
// transport a host launches as a subprocess). It blocks until stdin reaches EOF.
//
// Anything written to stdout must be protocol frames only, so send logs to
// stderr.
func (s *Server) ServeStdio(ctx context.Context, opts ...StdioOption) error {
	return s.ServeStream(ctx, os.Stdin, os.Stdout, opts...)
}

// ServeStream serves the MCP protocol over an arbitrary pair of
// newline-delimited JSON-RPC 2.0 streams. Use it for in-process pipes or any
// transport that is not the process's own stdio; ServeStdio wraps it for the
// common case.
func (s *Server) ServeStream(ctx context.Context, in io.Reader, out io.Writer, opts ...StdioOption) error {
	cfg := stdioConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.showAll {
		ctx = WithShowAllTools(ctx)
	}
	return s.newStdioDispatcher().ServeStream(ctx, in, out)
}

// newStdioDispatcher builds the jsonrpc.Server that frames and dispatches MCP
// methods over a stream. The MCP protocol is JSON-RPC 2.0, so the framing,
// batching and notification handling are provided by the jsonrpc package; the
// handlers here reuse the same dispatch logic (ListToolsWithContext, CallTool)
// as the HTTP transport. Unknown notifications (e.g. notifications/initialized)
// are ignored automatically by the jsonrpc server.
func (s *Server) newStdioDispatcher() *jsonrpc.Server {
	srv := jsonrpc.NewServer()

	srv.Handle("initialize", func(ctx context.Context, params json.RawMessage) (any, error) {
		return s.stdioInitialize(params)
	})

	srv.Handle("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})

	srv.Handle("tools/list", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]any{"tools": s.ListToolsWithContext(ctx)}, nil
	})

	srv.Handle("tools/call", func(ctx context.Context, params json.RawMessage) (any, error) {
		return s.stdioToolsCall(ctx, params)
	})

	return srv
}

func (s *Server) stdioInitialize(raw json.RawMessage) (any, error) {
	var params initializeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Invalid params", nil)
		}
	}

	protocolVersion := MCPProtocolVersionLatest
	if params.ProtocolVersion != "" {
		if !isSupportedProtocolVersion(params.ProtocolVersion) {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Unsupported protocol version", map[string]any{
				"requested": params.ProtocolVersion,
				"supported": supportedProtocolVersions,
			})
		}
		protocolVersion = params.ProtocolVersion
	}

	s.mu.RLock()
	instructions := s.instructions
	s.mu.RUnlock()

	return initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    s.buildCapabilities(protocolVersion),
		ServerInfo: serverInfo{
			Name:    s.name,
			Version: s.version,
		},
		Instructions: instructions,
	}, nil
}

func (s *Server) stdioToolsCall(ctx context.Context, raw json.RawMessage) (any, error) {
	var params ToolCallParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Invalid params", nil)
		}
	}

	response, err := s.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		if toolErr, ok := err.(*ToolError); ok {
			return nil, jsonrpc.NewError(toolErr.Code, toolErr.Message, toolErr.Data)
		}
		return nil, jsonrpc.NewError(ErrorCodeInternalError, fmt.Sprintf("Tool execution failed: %v", err), nil)
	}

	return ToolResult{
		Content:           response.Content,
		StructuredContent: response.StructuredContent,
		IsError:           false,
	}, nil
}
