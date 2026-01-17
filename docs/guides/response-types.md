# Response Types Guide

The MCP server supports multiple response types for different use cases:

## Text Response

Simple text output:

```go
return mcp.NewToolResponseText("Hello, world!")
```

Best for: Status messages, simple results, text content

## Image Response

Return images with automatic base64 encoding:

```go
imageBytes, _ := os.ReadFile("image.png")
return mcp.NewToolResponseImage(imageBytes, "image/png")
```

Supported MIME types: `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/svg+xml`

Best for: Generated images, screenshots, visualizations

## Audio Response

Return audio with automatic base64 encoding:

```go
audioBytes, _ := os.ReadFile("audio.wav")
return mcp.NewToolResponseAudio(audioBytes, "audio/wav")
```

Supported MIME types: `audio/wav`, `audio/mp3`, `audio/aac`, `audio/ogg`, `audio/flac`

Best for: Generated audio, recordings, voice responses

## Resource Response

Reference external resources:

```go
return mcp.NewToolResponseResource("file://path", "content", "text/plain")
```

Best for: File paths, URLs, cached content

## Resource Link Response

Point to external URLs:

```go
return mcp.NewToolResponseResourceLink("https://example.com", "View details")
```

Best for: Documentation links, external references, web content

## Structured Response

Return JSON-structured data:

```go
data := map[string]interface{}{
    "status": "success",
    "count": 42,
    "items": []string{"a", "b", "c"},
}
return mcp.NewToolResponseStructured(data)
```

Best for: Querying data, API responses, complex results

## Multi-Content Response

Combine multiple content types:

```go
response1 := mcp.NewToolResponseText("Results:")
response2 := mcp.NewToolResponseImage(imageBytes, "image/png")
response3 := mcp.NewToolResponseStructured(data)

return mcp.NewToolResponseMulti(response1, response2, response3)
```

Best for: Rich results combining text, images, and structured data

## Error Responses

### Invalid Parameter Error

```go
if name == "" {
    return nil, mcp.NewToolErrorInvalidParams("name parameter is required")
}
```

Use when: Client passes invalid arguments

### Internal Server Error

```go
if err := someOperation(); err != nil {
    return nil, mcp.NewToolErrorInternal("failed to process request")
}
```

Use when: Server-side operation fails

### Custom Error

```go
return nil, mcp.NewToolError(-32000, "Custom server error", map[string]interface{}{
    "details": "Additional error information",
    "code": "specific_error_code",
})
```

Use when: Custom error handling needed

## Response Guidelines

### Choose by Content Type

| Content Type    | Response Type                   | Use Case                        |
| --------------- | ------------------------------- | ------------------------------- |
| Plain text      | `NewToolResponseText()`         | Status, messages, descriptions  |
| Images          | `NewToolResponseImage()`        | Graphics, diagrams, screenshots |
| Audio           | `NewToolResponseAudio()`        | Voice, recordings, sound        |
| JSON/structured | `NewToolResponseStructured()`   | Data, results, API responses    |
| Web content     | `NewToolResponseResourceLink()` | URLs, documentation             |
| Files           | `NewToolResponseResource()`     | File paths, cached content      |
| Mixed           | `NewToolResponseMulti()`        | Rich content                    |

### Size Considerations

- **Images**: Keep under 1MB, consider compression
- **Audio**: Keep under 5MB, consider compression
- **Structured data**: Keep response reasonable (< 10MB)
- **Text**: Unlimited but consider token limits

### Base64 Encoding

Images and audio are automatically base64 encoded. The library handles this transparently:

```go
imageBytes, _ := os.ReadFile("image.png")
// No need to manually base64 encode - the library does this automatically
return mcp.NewToolResponseImage(imageBytes, "image/png")
```

## Example: Complex Response

```go
func handleAnalysis(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    data, _ := req.String("data")

    // Run analysis
    results := analyze(data)

    if len(results.Errors) > 0 {
        return nil, mcp.NewToolErrorInvalidParams(fmt.Sprintf("Analysis failed: %v", results.Errors))
    }

    // Generate visualization
    imageBytes := generateChart(results)

    // Return multi-content response
    textResp := mcp.NewToolResponseText(fmt.Sprintf("Analysis complete: %d items processed", len(results.Items)))
    imageResp := mcp.NewToolResponseImage(imageBytes, "image/png")
    dataResp := mcp.NewToolResponseStructured(results)

    return mcp.NewToolResponseMulti(textResp, imageResp, dataResp), nil
}
```

## See Also

- [Error Handling Guide](error-handling.md) - Error patterns and best practices
- Tool Providers Guide - Custom response formatting per provider
