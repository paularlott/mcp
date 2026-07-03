package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/paularlott/jsonrpc"
)

// stdioTransport implements clientTransport over a newline-delimited JSON-RPC
// stream using the jsonrpc client. It backs both an in-process stream client
// and a spawned subprocess client.
type stdioTransport struct {
	rpc *jsonrpc.Client
}

func (t *stdioTransport) roundTrip(ctx context.Context, req *MCPRequest, resp *MCPResponse, respHeaders *http.Header) error {
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	var result json.RawMessage
	err := t.rpc.Call(ctx, req.Method, req.Params, &result)
	if err != nil {
		var rpcErr *jsonrpc.Error
		if errors.As(err, &rpcErr) {
			resp.Error = &MCPError{Code: rpcErr.Code, Message: rpcErr.Message, Data: rpcErr.Data}
			return nil
		}
		return err
	}

	if len(result) > 0 {
		var v any
		if err := json.Unmarshal(result, &v); err != nil {
			return err
		}
		resp.Result = v
	}
	if respHeaders != nil {
		*respHeaders = http.Header{}
	}
	return nil
}

func (t *stdioTransport) Close() error {
	return t.rpc.Close()
}

// batchRoundTrip implements batchTransport by sending every request as a
// single JSON-RPC batch via jsonrpc.Client.CallBatch. Results are returned in
// the same order as reqs (CallBatch's own guarantee).
func (t *stdioTransport) batchRoundTrip(ctx context.Context, reqs []*MCPRequest) ([]*MCPResponse, error) {
	calls := make([]jsonrpc.BatchCall, len(reqs))
	raws := make([]json.RawMessage, len(reqs))
	for i, req := range reqs {
		calls[i] = jsonrpc.BatchCall{Method: req.Method, Params: req.Params, Out: &raws[i]}
	}

	batchResults := t.rpc.CallBatch(ctx, calls)

	resps := make([]*MCPResponse, len(reqs))
	for i, req := range reqs {
		resp := &MCPResponse{JSONRPC: "2.0", ID: req.ID}
		if err := batchResults[i].Err; err != nil {
			var rpcErr *jsonrpc.Error
			if errors.As(err, &rpcErr) {
				resp.Error = &MCPError{Code: rpcErr.Code, Message: rpcErr.Message, Data: rpcErr.Data}
			} else {
				return nil, err
			}
		} else if len(raws[i]) > 0 {
			var v any
			if err := json.Unmarshal(raws[i], &v); err != nil {
				return nil, err
			}
			resp.Result = v
		}
		resps[i] = resp
	}
	return resps, nil
}

// StdioClientOption configures a subprocess-backed stdio client.
type StdioClientOption func(*stdioClientConfig)

type stdioClientConfig struct {
	stderr io.Writer
	env    []string
	dir    string
	onExit func(error)
}

// WithClientStderr routes the child server's standard error to w (default:
// inherited from the parent). Pass io.Discard to silence it.
func WithClientStderr(w io.Writer) StdioClientOption {
	return func(c *stdioClientConfig) { c.stderr = w }
}

// WithClientEnv sets the environment for the child server process.
func WithClientEnv(env []string) StdioClientOption {
	return func(c *stdioClientConfig) { c.env = env }
}

// WithClientDir sets the working directory for the child server process.
func WithClientDir(dir string) StdioClientOption {
	return func(c *stdioClientConfig) { c.dir = dir }
}

// WithClientOnExit registers a callback invoked exactly once when the spawned
// server process exits — whether it crashes mid-session or is shut down by
// [Client.Close]. It is invoked asynchronously from a reaper goroutine, so it
// fires even if Close is never called; it must not block.
//
// Without this, a caller has no way to learn that its child server has died
// other than subsequent calls failing. This is passed through to
// [jsonrpc.WithOnExit].
func WithClientOnExit(fn func(error)) StdioClientOption {
	return func(c *stdioClientConfig) { c.onExit = fn }
}

// NewStdioClient launches command (with args) as an MCP server speaking
// newline-delimited JSON-RPC over its stdin/stdout, and returns a client
// connected to it. Call Close to shut the child process down.
//
// The namespace behaves as for NewClient: when non-empty it is prefixed to tool
// names (e.g. namespace "fs" exposes tool "read" as "fs__read").
func NewStdioClient(command string, args []string, namespace string, opts ...StdioClientOption) (*Client, error) {
	cfg := stdioClientConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	procOpts := []jsonrpc.ProcessOption{}
	if cfg.stderr != nil {
		procOpts = append(procOpts, jsonrpc.WithStderr(cfg.stderr))
	}
	if cfg.env != nil {
		procOpts = append(procOpts, jsonrpc.WithEnv(cfg.env))
	}
	if cfg.dir != "" {
		procOpts = append(procOpts, jsonrpc.WithDir(cfg.dir))
	}
	if cfg.onExit != nil {
		procOpts = append(procOpts, jsonrpc.WithOnExit(cfg.onExit))
	}

	proc, err := jsonrpc.NewProcessTransport(command, args, procOpts...)
	if err != nil {
		return nil, err
	}
	return newStreamClient(jsonrpc.NewClient(proc), namespace), nil
}

// NewStreamClient returns an MCP client that speaks newline-delimited JSON-RPC
// over the given streams: it writes requests to out and reads responses from
// in. Use it to connect to a server exposed via Server.ServeStream (for example
// over an in-process pipe), or any transport you manage yourself. Close stops
// the client's reader.
func NewStreamClient(in io.Reader, out io.Writer, namespace string) *Client {
	transport := jsonrpc.NewStreamTransport(in, out)
	return newStreamClient(jsonrpc.NewClient(transport), namespace)
}

// newStreamClient builds a Client whose round-trips go through the jsonrpc
// client, applying the same namespace normalisation as NewClientWithPool.
func newStreamClient(rpc *jsonrpc.Client, namespace string) *Client {
	separator := DefaultNamespaceSeparator
	namespace = strings.TrimSpace(namespace)
	if namespace != "" && !strings.HasSuffix(namespace, separator) {
		namespace = namespace + separator
	}
	return &Client{
		namespace: namespace,
		separator: separator,
		transport: &stdioTransport{rpc: rpc},
	}
}
