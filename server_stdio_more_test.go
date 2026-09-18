package mcp_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/paularlott/mcp"
)

// pipeStdioServer wires a stream client to a ServeStream server (with the
// given options) over two in-process pipes and returns the client plus a
// cleanup function. It mirrors pipeStdioClient in stdio_test.go but allows
// passing StdioOptions, needed to exercise WithStdioShowAllTools.
func pipeStdioServer(t *testing.T, s *mcp.Server, opts ...mcp.StdioOption) (*mcp.Client, func()) {
	t.Helper()
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = s.ServeStream(context.Background(), serverReader, serverWriter, opts...)
	}()

	client := mcp.NewStreamClient(clientReader, clientWriter, "")
	cleanup := func() {
		client.Close()
		clientWriter.Close()
		<-serveDone
		serverWriter.Close()
	}
	return client, cleanup
}

// TestStdio_WithStdioShowAllTools verifies that the show-all stdio option
// makes discoverable tools appear in tools/list over the stream, matching the
// HTTP show-all behaviour, while the discovery meta-tools stay hidden.
func TestStdio_WithStdioShowAllTools(t *testing.T) {
	s := mcp.NewServer("stdio-showall", "1.0")
	s.RegisterTool(mcp.NewTool("native_tool", "native"), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText("native"), nil
	})
	s.RegisterTool(mcp.NewTool("disc_tool", "discoverable").Discoverable("kw"), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText("disc"), nil
	})

	client, cleanup := pipeStdioServer(t, s, mcp.WithStdioShowAllTools())
	defer cleanup()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["native_tool"] {
		t.Errorf("expected native_tool, got %+v", tools)
	}
	if !names["disc_tool"] {
		t.Errorf("expected disc_tool visible under show-all, got %+v", tools)
	}
	if names[mcp.ToolSearchName] || names[mcp.ExecuteToolName] {
		t.Errorf("meta-tools should be hidden under show-all, got %+v", tools)
	}
}

// TestStdio_ResourcesRead covers stdioResourcesRead's success, missing-uri,
// and unknown-resource paths via Client.ReadResource over the stdio
// transport (no existing test drives resources/read through stdio).
func TestStdio_ResourcesRead(t *testing.T) {
	s := mcp.NewServer("stdio-resources", "1.0")
	s.RegisterResource(
		mcp.NewResource("config://app", "App Config", "the config", "text/plain"),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			return mcp.NewResourceResponseText(req.URI(), "hello-stdio", "text/plain"), nil
		},
	)
	client, cleanup := pipeStdioServer(t, s)
	defer cleanup()

	ctx := context.Background()
	resp, err := client.ReadResource(ctx, "config://app")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(resp.Contents) != 1 || resp.Contents[0].Text != "hello-stdio" {
		t.Fatalf("unexpected resource response: %+v", resp)
	}

	if _, err := client.ReadResource(ctx, "config://missing"); err == nil {
		t.Error("expected an error for an unknown resource")
	}

	if _, err := client.ReadResource(ctx, ""); err == nil {
		t.Error("expected an error for an empty uri")
	}
}

// TestStdio_PromptsGet covers stdioPromptsGet's success, missing-name, and
// unknown-prompt paths via Client.GetPrompt over the stdio transport.
func TestStdio_PromptsGet(t *testing.T) {
	s := mcp.NewServer("stdio-prompts", "1.0")
	s.RegisterPrompt(
		mcp.NewPrompt("greet", "Greet someone").Argument("name", "who", true),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			name, _ := req.String("name")
			return &mcp.PromptResponse{
				Messages: []mcp.PromptMessage{
					{Role: mcp.PromptRoleUser, Content: mcp.PromptMessageContent{Type: "text", Text: "Hi " + name}},
				},
			}, nil
		},
	)
	client, cleanup := pipeStdioServer(t, s)
	defer cleanup()

	ctx := context.Background()
	resp, err := client.GetPrompt(ctx, "greet", map[string]string{"name": "Bob"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].Content.Text != "Hi Bob" {
		t.Fatalf("unexpected prompt response: %+v", resp)
	}

	if _, err := client.GetPrompt(ctx, "no_such_prompt", nil); err == nil {
		t.Error("expected an error for an unknown prompt")
	}

	if _, err := client.GetPrompt(ctx, "", nil); err == nil {
		t.Error("expected an error for an empty prompt name")
	}
}

// TestStdioSubprocessWithClientDir verifies WithClientDir sets the spawned
// child server's working directory: the "cwd" tool (added below by spawning
// this same test binary) reports the directory we asked for.
func TestStdioSubprocessWithClientDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "mcp-client-dir-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)
	// Resolve symlinks (e.g. on macOS /tmp -> /private/tmp) so the comparison
	// against the child's os.Getwd() (which returns the resolved path) matches.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	client, err := mcp.NewStdioClient(
		os.Args[0], nil, "",
		mcp.WithClientEnv(append(os.Environ(), stdioChildEnv+"=1")),
		mcp.WithClientStderr(io.Discard),
		mcp.WithClientDir(resolvedDir),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.CallTool(ctx, "cwd", nil)
	if err != nil {
		t.Fatalf("CallTool(cwd): %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != resolvedDir {
		t.Errorf("child cwd = %q, want %q", contentTextOrEmpty(resp), resolvedDir)
	}
}

func contentTextOrEmpty(resp *mcp.ToolResponse) string {
	if resp == nil || len(resp.Content) == 0 {
		return ""
	}
	return resp.Content[0].Text
}
