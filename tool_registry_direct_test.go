package mcp

import "context"

// fakeToolProvider is a minimal ToolProvider for provider-delegation tests
// (the higher-level provider tests in tool_provider_test.go exercise the
// free functions like listToolsFromProviders instead).
type fakeToolProvider struct {
	tools []MCPTool
	err   error

	execName string
	execResp *ToolResponse
	execErr  error
}

func (f *fakeToolProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *fakeToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	if name == f.execName {
		return f.execResp, nil
	}
	return nil, ErrUnknownTool
}
