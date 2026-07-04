package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

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
//
// The read loop runs here (in mcp) rather than in jsonrpc.Server.ServeStream so
// that outbound notifications can share the stream's write mutex, and so the
// caller's ctx (which carries the show-all flag) is threaded to handlers.
func (s *Server) ServeStream(ctx context.Context, in io.Reader, out io.Writer, opts ...StdioOption) error {
	cfg := stdioConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.showAll {
		ctx = WithShowAllTools(ctx)
	}

	srv := s.newStdioDispatcher()

	// A shared write mutex guards out so responses and notifications serialize.
	var writeMu sync.Mutex
	var wg sync.WaitGroup

	// The sink is non-blocking: it queues notifications to a buffered channel and
	// a drainer writes them under writeMu. This keeps emit (which can run under
	// s.mu from RegisterTool) from ever blocking on a slow/stuck client.
	sink := newStreamNotifySink(out, &writeMu)
	subID := s.notifications.subscribe(sink)
	defer s.notifications.unsubscribe(subID)
	defer sink.stop()

	// Close the reader on ctx cancellation so decoder.Decode unblocks. Without
	// this, Ctrl+C cancels the context but the read loop is stuck in Decode and
	// the process hangs. os.Stdin (the common case) is an io.Closer.
	if closer, ok := in.(io.Closer); ok {
		go func() {
			<-ctx.Done()
			closer.Close()
		}()
	}

	decoder := json.NewDecoder(in)
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			wg.Wait() // let in-flight handlers flush their responses
			if ctx.Err() != nil {
				return nil // context cancelled (reader closed) — clean shutdown
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		wg.Add(1)
		go func(msg json.RawMessage) {
			defer wg.Done()
			if resp, hasResp := srv.HandleMessage(ctx, msg); hasResp {
				writeMu.Lock()
				_, _ = out.Write(append(resp, '\n'))
				writeMu.Unlock()
			}
		}(raw)
	}
}

// streamNotifySink is a notificationSink that writes JSON-RPC notifications to a
// stdio stream. send is non-blocking: it enqueues to a buffered channel and a
// drain goroutine does the actual write under the shared response mutex. This
// keeps a slow/stuck client from stalling notification emission (which can run
// under s.mu via RegisterTool). Events are dropped if the buffer fills.
type streamNotifySink struct {
	ch   chan sseEvent
	out  io.Writer
	mu   *sync.Mutex
	done chan struct{}
}

func newStreamNotifySink(out io.Writer, mu *sync.Mutex) *streamNotifySink {
	sn := &streamNotifySink{
		ch:   make(chan sseEvent, 64),
		out:  out,
		mu:   mu,
		done: make(chan struct{}),
	}
	go sn.drain()
	return sn
}

func (sn *streamNotifySink) send(method string, params any) {
	select {
	case sn.ch <- sseEvent{method: method, params: params}:
	default: // buffer full: drop (listChanged is an idempotent re-fetch hint)
	}
}

func (sn *streamNotifySink) drain() {
	for {
		select {
		case ev := <-sn.ch:
			payload, err := json.Marshal(MCPNotification{JSONRPC: "2.0", Method: ev.method, Params: ev.params})
			if err != nil {
				continue
			}
			sn.mu.Lock()
			_, _ = sn.out.Write(append(payload, '\n'))
			sn.mu.Unlock()
		case <-sn.done:
			return
		}
	}
}

func (sn *streamNotifySink) stop() { close(sn.done) }

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

	srv.Handle("resources/list", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]any{"resources": s.ListResources(ctx)}, nil
	})

	srv.Handle("resources/templates/list", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]any{"resourceTemplates": s.ListResourceTemplates(ctx)}, nil
	})

	srv.Handle("resources/read", func(ctx context.Context, params json.RawMessage) (any, error) {
		return s.stdioResourcesRead(ctx, params)
	})

	srv.Handle("prompts/list", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]any{"prompts": s.ListPrompts(ctx)}, nil
	})

	srv.Handle("prompts/get", func(ctx context.Context, params json.RawMessage) (any, error) {
		return s.stdioPromptsGet(ctx, params)
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

func (s *Server) stdioResourcesRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var params resourceReadParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Invalid params", nil)
		}
	}
	if params.URI == "" {
		return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "uri parameter is required", nil)
	}

	resp, err := s.ReadResource(ctx, params.URI)
	if err != nil {
		if err == ErrUnknownResource {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Resource not found", nil)
		}
		if toolErr, ok := err.(*ToolError); ok {
			return nil, jsonrpc.NewError(toolErr.Code, toolErr.Message, toolErr.Data)
		}
		return nil, jsonrpc.NewError(ErrorCodeInternalError, fmt.Sprintf("Resource read failed: %v", err), nil)
	}
	return resp, nil
}

func (s *Server) stdioPromptsGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var params promptGetParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Invalid params", nil)
		}
	}
	if params.Name == "" {
		return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "name parameter is required", nil)
	}

	resp, err := s.GetPrompt(ctx, params.Name, params.Arguments)
	if err != nil {
		if err == ErrUnknownPrompt {
			return nil, jsonrpc.NewError(ErrorCodeInvalidParams, "Prompt not found", nil)
		}
		if toolErr, ok := err.(*ToolError); ok {
			return nil, jsonrpc.NewError(toolErr.Code, toolErr.Message, toolErr.Data)
		}
		return nil, jsonrpc.NewError(ErrorCodeInternalError, fmt.Sprintf("Prompt render failed: %v", err), nil)
	}
	return resp, nil
}
