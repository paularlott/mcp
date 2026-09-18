package mcp

import (
	"context"
	"io"
	"testing"

	"github.com/paularlott/jsonrpc"
)

// TestNewStreamClient_Internal covers the unexported newStreamClient helper
// (retained for callers with an already-connected jsonrpc.Client), which no
// existing test calls directly — NewStreamClient and NewStdioClient both go
// through newPeerClient instead. Wires a jsonrpc.StreamTransport-backed
// client to a server via ServeStream over in-process pipes.
func TestNewStreamClient_Internal(t *testing.T) {
	s := NewServer("internal-stream-test", "1.0")
	s.RegisterTool(NewTool("ping_tool", "returns pong"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("pong"), nil
	})

	clientReader, serverWriter := io.Pipe() // server -> client
	serverReader, clientWriter := io.Pipe() // client -> server

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = s.ServeStream(context.Background(), serverReader, serverWriter)
	}()

	transport := jsonrpc.NewStreamTransport(clientReader, clientWriter)
	rpc := jsonrpc.NewClient(transport)
	client := newStreamClient(rpc, "ns")

	defer func() {
		client.Close()
		clientWriter.Close()
		<-serveDone
		serverWriter.Close()
	}()

	if got, want := client.Namespace(), "ns__"; got != want {
		t.Errorf("Namespace() = %q, want %q", got, want)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ns__ping_tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	resp, err := client.CallTool(context.Background(), "ns__ping_tool", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "pong" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// TestWithClientDir covers the WithClientDir option setter directly (its
// effect on the spawned process's working directory is exercised implicitly
// by any subprocess test, but no test sets it and checks the stored config).
func TestWithClientDir(t *testing.T) {
	cfg := &stdioClientConfig{}
	WithClientDir("/tmp/some-dir")(cfg)
	if cfg.dir != "/tmp/some-dir" {
		t.Errorf("cfg.dir = %q, want /tmp/some-dir", cfg.dir)
	}
}
