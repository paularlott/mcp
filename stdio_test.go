package mcp_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/paularlott/mcp"
)

// buildStdioTestServer creates a small MCP server used by both the in-process
// pipe tests and the spawned-subprocess test.
func buildStdioTestServer() *mcp.Server {
	s := mcp.NewServer("stdio-test-server", "1.2.3")
	s.SetInstructions("stdio test instructions")

	s.RegisterTool(
		mcp.NewTool("greet", "Greet someone by name",
			mcp.String("name", "the name to greet", mcp.Required()),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, err := req.String("name")
			if err != nil {
				return nil, mcp.NewToolErrorInvalidParams("name is required")
			}
			return mcp.NewToolResponseText("Hello, " + name + "!"), nil
		},
	)

	s.RegisterTool(
		mcp.NewTool("boom", "Always fails"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return nil, mcp.NewToolErrorInternal("kaboom")
		},
	)

	return s
}

const stdioChildEnv = "MCP_TEST_STDIO_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(stdioChildEnv) == "1" {
		// Child mode: act as an MCP stdio server.
		_ = buildStdioTestServer().ServeStdio(context.Background())
		return
	}
	os.Exit(m.Run())
}

// pipeStdioClient wires a stream client to a ServeStream server over two
// in-process pipes and returns the client plus a cleanup function.
func pipeStdioClient(t *testing.T, s *mcp.Server) (*mcp.Client, func()) {
	t.Helper()
	clientReader, serverWriter := io.Pipe() // server -> client
	serverReader, clientWriter := io.Pipe() // client -> server

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = s.ServeStream(context.Background(), serverReader, serverWriter)
	}()

	client := mcp.NewStreamClient(clientReader, clientWriter, "")

	cleanup := func() {
		client.Close()
		clientWriter.Close() // EOF -> server ServeStream returns
		<-serveDone
		serverWriter.Close() // EOF -> client reader loop exits
	}
	return client, cleanup
}

func TestStdioPipeListTools(t *testing.T) {
	client, cleanup := pipeStdioClient(t, buildStdioTestServer())
	defer cleanup()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["greet"] || !names["boom"] {
		t.Errorf("missing expected tools, got %v", names)
	}
}

func TestStdioPipeCallTool(t *testing.T) {
	client, cleanup := pipeStdioClient(t, buildStdioTestServer())
	defer cleanup()

	resp, err := client.CallTool(context.Background(), "greet", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "Hello, Ada!" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
}

func TestStdioPipeToolError(t *testing.T) {
	client, cleanup := pipeStdioClient(t, buildStdioTestServer())
	defer cleanup()

	_, err := client.CallTool(context.Background(), "boom", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var toolErr *mcp.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected *mcp.ToolError, got %T: %v", err, err)
	}
	if toolErr.Code != mcp.ErrorCodeInternalError {
		t.Errorf("code = %d, want %d", toolErr.Code, mcp.ErrorCodeInternalError)
	}
	if toolErr.Message != "kaboom" {
		t.Errorf("message = %q, want kaboom", toolErr.Message)
	}
}

func TestStdioPipeNamespace(t *testing.T) {
	// A namespaced client sees tool names prefixed, and strips the prefix on call.
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = buildStdioTestServer().ServeStream(context.Background(), serverReader, serverWriter)
	}()
	client := mcp.NewStreamClient(clientReader, clientWriter, "fs")
	defer func() {
		client.Close()
		clientWriter.Close()
		<-serveDone
		serverWriter.Close()
	}()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == "fs__greet" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected namespaced tool fs__greet, got %+v", tools)
	}

	resp, err := client.CallTool(context.Background(), "fs__greet", map[string]any{"name": "Bob"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.Content[0].Text != "Hello, Bob!" {
		t.Errorf("unexpected content: %+v", resp.Content)
	}
}

// TestStdioSubprocess spawns this test binary as an MCP stdio server and drives
// it through NewStdioClient, exercising the real subprocess transport.
func TestStdioSubprocess(t *testing.T) {
	client, err := mcp.NewStdioClient(
		os.Args[0], nil, "",
		mcp.WithClientEnv(append(os.Environ(), stdioChildEnv+"=1")),
		mcp.WithClientStderr(io.Discard),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	resp, err := client.CallTool(ctx, "greet", map[string]any{"name": "Grace"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.Content[0].Text != "Hello, Grace!" {
		t.Errorf("unexpected content: %+v", resp.Content)
	}
}

// TestStdioSubprocessOnExit verifies the WithClientOnExit hook fires once when
// the spawned server is shut down via Close.
func TestStdioSubprocessOnExit(t *testing.T) {
	exited := make(chan error, 1)
	client, err := mcp.NewStdioClient(
		os.Args[0], nil, "",
		mcp.WithClientEnv(append(os.Environ(), stdioChildEnv+"=1")),
		mcp.WithClientStderr(io.Discard),
		mcp.WithClientOnExit(func(err error) { exited <- err }),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("onExit err = %v, want nil for clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WithClientOnExit did not fire after Close")
	}
}
