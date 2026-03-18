# MCP Documentation

## Guides

- **[Tool Providers](./guides/tool-providers.md)** — Dynamic per-request tools, multi-tenant isolation, tool visibility, and show-all mode
- **[Tool Discovery](./guides/tool-discovery.md)** — Searchable tools, `tool_search`, `execute_tool`, and context window optimisation
- **[Remote Servers](./guides/remote-servers.md)** — Connecting to remote MCP servers, namespacing, filtering, and parallel tool calls
- **[Sessions](./guides/sessions.md)** — JWT session management (MCP 2025-11-25)
- **[Response Types](./guides/response-types.md)** — Text, images, audio, structured, and multi-content responses
- **[Error Handling](./guides/error-handling.md)** — Error types, codes, and best practices
- **[Thread Safety & Concurrency](./guides/concurrency.md)** — Safe concurrent usage and parallel client calls
- **[Protocol Support](./guides/protocol-support.md)** — Supported MCP protocol versions and negotiation

## Examples

See [../examples](../examples/) for complete, runnable examples:

- [Basic Server](../examples/server/)
- [Multi-Tenant Tools](../examples/per-user-tools/)
- [Tool Discovery](../examples/tool-discovery/)
- [Remote Servers](../examples/remote-server/)
- [Session Management](../examples/session-server/)
- [OpenAI Integration](../examples/openai/)
