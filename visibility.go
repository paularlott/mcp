package mcp

// ToolVisibility defines how a tool is exposed to clients.
// This controls whether tools appear in tools/list or only via tool_search.
type ToolVisibility int

const (
	// ToolVisibilityNative means the tool appears in tools/list and is directly callable.
	// This is the standard MCP behavior - tools are visible and can be called by name.
	ToolVisibilityNative ToolVisibility = iota

	// ToolVisibilityOnDemand means the tool is only available via tool_search and execute_tool.
	// The tool does NOT appear in tools/list but can be discovered and executed through
	// the tool_search and execute_tool meta-tools. This is useful for:
	// - Large tool sets where listing all tools would overwhelm the LLM
	// - Dynamic tools that should be discovered by keyword search
	// - Tools that should only be used when specifically relevant
	ToolVisibilityOnDemand
)

// String returns a human-readable name for the visibility level.
func (v ToolVisibility) String() string {
	switch v {
	case ToolVisibilityNative:
		return "native"
	case ToolVisibilityOnDemand:
		return "ondemand"
	default:
		return "unknown"
	}
}
