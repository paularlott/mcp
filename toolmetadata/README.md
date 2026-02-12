# Tool Metadata

A standard format for defining MCP tool metadata that can be loaded from any source (TOML, JSON, YAML, database, API, etc.).

## Format

The metadata format consists of two main structures:

### ToolMetadata

```go
type ToolMetadata struct {
    Description  string           // Tool description
    Keywords     []string         // Keywords for discovery mode
    Parameters   []ToolParameter  // Tool parameters
    Discoverable bool            // Enable discovery mode
}
```

### ToolParameter

```go
type ToolParameter struct {
    Name        string  // Parameter name
    Type        string  // Parameter type: "string", "int", "float", "bool"
    Description string  // Parameter description
    Required    bool    // Whether parameter is required
}
```

## TOML Example

```toml
description = "Execute a shell command and return the output"
keywords = ["shell", "command", "execute", "run"]
discoverable = true

[[parameters]]
name = "command"
type = "string"
description = "The shell command to execute"
required = true

[[parameters]]
name = "args"
type = "array:string"
description = "Command arguments"
required = false

[[parameters]]
name = "timeout"
type = "int"
description = "Timeout in seconds (default: 30)"
required = false
```

## Usage Example

```go
package main

import (
    "github.com/paularlott/mcp"
    "github.com/paularlott/mcp/toolmetadata"
    "github.com/BurntSushi/toml"
)

func main() {
    // Parse TOML file
    var meta toolmetadata.ToolMetadata
    if _, err := toml.DecodeFile("execute_command.toml", &meta); err != nil {
        panic(err)
    }

    // Build MCP tool
    tool := toolmetadata.BuildMCPTool("execute_command", &meta)

    // Register with MCP server
    server := mcp.NewServer()
    server.RegisterTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        // Tool implementation
        return mcp.NewToolResultText("output"), nil
    })
}
```

## JSON Example

```json
{
  "description": "Execute a shell command and return the output",
  "keywords": ["shell", "command", "execute", "run"],
  "discoverable": true,
  "parameters": [
    {
      "name": "command",
      "type": "string",
      "description": "The shell command to execute",
      "required": true
    },
    {
      "name": "timeout",
      "type": "int",
      "description": "Timeout in seconds (default: 30)",
      "required": false
    }
  ]
}
```

## Supported Parameter Types

- `string` - String parameter
- `int`, `integer` - Integer/number parameter
- `float`, `number` - Float/number parameter
- `bool`, `boolean` - Boolean parameter
- `array:string` - Array of strings
- `array:int`, `array:integer`, `array:number`, `array:float` - Array of numbers
- `array:bool`, `array:boolean` - Array of booleans

Unknown types default to string parameters.
