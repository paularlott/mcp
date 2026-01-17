# MCP Documentation

Welcome to the MCP documentation. Here you'll find comprehensive guides for building and deploying Model Context Protocol servers in Go.

## Core Guides

- **[Tool Providers](./guides/tool-providers.md)** - Dynamic tool loading, per-request providers, and request-scoped tools
- **[Dynamic Tool States](./guides/dynamic-tool-states.md)** - Transitioning tools between native and on-demand visibility modes
- **[Tool Discovery and Search](./guides/tool-discovery.md)** - Searchable tools, tool_search, and execute_tool patterns
- **[Remote Servers](./guides/remote-servers.md)** - Connecting to and proxying remote MCP servers
- **[Session Management](./guides/sessions.md)** - JWT and Redis session management (MCP 2025-11-25)
- **[Protocol Support](./guides/protocol-support.md)** - MCP protocol versions and features

## Advanced Topics

- **[Error Handling](./guides/error-handling.md)** - ToolError, MCP error codes, and error responses
- **[Response Types](./guides/response-types.md)** - Text, structured content, resources, images, and TOON encoding
- **[Thread Safety & Concurrency](./guides/concurrency.md)** - Safe concurrent usage patterns and goroutine safety

## Quick References

- **[Dynamic Tool States Quick Ref](./quick-ref/dynamic-tool-states.md)** - Quick start for tool state transitions

## Examples

See [../examples](../examples/) for complete, runnable examples:

- Basic server setup
- Multi-tenant tool providers
- Feature gating and progressive disclosure
- OpenAI integration
- Tool discovery with dynamic providers

## Frequently Asked Questions

**Q: When should I use dynamic tool providers?**
A: Use them when you need per-request or per-tenant tool isolation, such as multi-tenant SaaS applications.

**Q: What's the difference between native and on-demand modes?**
A: Native mode shows all tools in `tools/list` (full context); on-demand mode hides tools and requires search (minimal context).

**Q: How do I handle tool visibility based on user permissions?**
A: Use conditional context creation - create different contexts in your HTTP middleware based on user roles.

**Q: Can tools be called if they're hidden in on-demand mode?**
A: Yes! Hidden tools remain callable via `execute_tool` or `CallTool()`, they're just not listed.

## Contributing

Found a documentation issue or have a suggestion? See [../CONTRIBUTING.md](../CONTRIBUTING.md).

## License

MCP is licensed under the MIT License. See [../LICENSE.txt](../LICENSE.txt).
