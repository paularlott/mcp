// Command server is the fixture server for the official MCP conformance
// suite (github.com/modelcontextprotocol/conformance). It serves, on one
// endpoint and for every protocol revision the library speaks, the tools,
// resources and prompts the suite's server scenarios call by name, built
// only with this library's public API. See ../README.md for how to run the
// suite against it.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"time"

	"github.com/paularlott/mcp"
)

func main() {
	addr := flag.String("addr", "localhost:3001", "address to listen on; the endpoint is /mcp")
	flag.Parse()

	server, err := newServer()
	if err != nil {
		log.Fatal(err)
	}
	providers := []mcp.ToolProvider{schemaProvider{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		server.HandleRequest(w, r.WithContext(mcp.WithToolProviders(r.Context(), providers...)))
	})
	log.Printf("conformance server listening on http://%s/mcp", *addr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// newServer builds the server with every fixture registered.
func newServer() (*mcp.Server, error) {
	s := mcp.NewServer("mcp-go-conformance", "1.0.0")
	sessions, err := mcp.NewJWTSessionManagerWithAutoKey(time.Hour)
	if err != nil {
		return nil, err
	}
	s.SetSessionManager(sessions)
	registerTools(s)
	registerDiagnosticTools(s)
	registerResources(s)
	registerPrompts(s)
	registerElicitation(s)
	return s, nil
}

func registerTools(s *mcp.Server) {
	s.RegisterTool(mcp.NewTool("test_simple_text", "Returns a simple text response").
		Title("Simple text").ReadOnly().Idempotent().OpenWorld(false),
		text("This is a simple text response for testing."))

	s.RegisterTool(mcp.NewTool("test_image_content", "Returns an image"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseImage(redPixelPNG, "image/png"), nil
		})

	s.RegisterTool(mcp.NewTool("test_audio_content", "Returns audio"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseAudio(silentWAV, "audio/wav"), nil
		})

	s.RegisterTool(mcp.NewTool("test_embedded_resource", "Returns an embedded resource"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseResource("test://embedded-resource", "This is an embedded resource content.", "text/plain"), nil
		})

	s.RegisterTool(mcp.NewTool("test_multiple_content_types", "Returns text, an image and a resource"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseMulti(
				mcp.NewToolResponseText("Multiple content types test:"),
				mcp.NewToolResponseImage(redPixelPNG, "image/png"),
				mcp.NewToolResponseResource("test://mixed-content-resource", `{"test":"data","value":123}`, "application/json"),
			), nil
		})

	// A tool failure is a tool execution error: a result with isError set.
	s.RegisterTool(mcp.NewTool("test_error_handling", "Always fails"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseError("This tool intentionally returns an error for testing."), nil
		})

	// The SSE polling scenario calls this tool; the library answers on the
	// POST response (it does not close the stream mid-call), which the
	// scenario reports as informational.
	s.RegisterTool(mcp.NewTool("test_reconnection", "Completes after a short delay"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("Reconnection test completed"), nil
		})

	// Reports progress 0, 50 and 100 of 100 on the call's own stream, before
	// its result (a no-op when the request carried no progressToken).
	s.RegisterTool(mcp.NewTool("test_tool_with_progress", "Reports progress while it runs"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			for i, p := range []float64{0, 50, 100} {
				if i > 0 {
					select {
					case <-time.After(50 * time.Millisecond):
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				if err := mcp.ReportProgress(ctx, p, 100, fmt.Sprintf("Step %d of 3", i+1)); err != nil {
					return nil, err
				}
			}
			return mcp.NewToolResponseText("Progress test completed"), nil
		})
}

// registerDiagnosticTools adds the tools the 2026-07-28 scenarios use to
// probe server behaviour rather than content.
func registerDiagnosticTools(s *mcp.Server) {
	// Needs the client's sampling capability: a request that doesn't
	// declare it gets MissingRequiredClientCapabilityError (-32021).
	s.RegisterTool(mcp.NewTool("test_missing_capability", "Requires the client's sampling capability"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			if _, ok := mcp.ClientCapabilitiesFromContext(ctx)["sampling"]; !ok {
				return nil, mcp.NewToolErrorMissingClientCapability("sampling")
			}
			return mcp.NewToolResponseText("The client declared sampling."), nil
		})

	// Change the tool and prompt lists, so subscriptions/listen streams get
	// notifications/tools/list_changed and notifications/prompts/list_changed.
	s.RegisterTool(mcp.NewTool("test_trigger_tool_change", "Adds or removes a tool").
		Destructive(false).OpenWorld(false),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			if !s.UnregisterTool("test_dynamic_tool") {
				s.RegisterTool(mcp.NewTool("test_dynamic_tool", "Added by test_trigger_tool_change"), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
					return mcp.NewToolResponseText("dynamic"), nil
				})
			}
			return mcp.NewToolResponseText("Tool list changed"), nil
		})
	s.RegisterTool(mcp.NewTool("test_trigger_prompt_change", "Adds or removes a prompt"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			if !s.UnregisterPrompt("test_dynamic_prompt") {
				s.RegisterPrompt(mcp.NewPrompt("test_dynamic_prompt", "Added by test_trigger_prompt_change"), func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
					return mcp.NewPromptResponseText("dynamic"), nil
				})
			}
			return mcp.NewToolResponseText("Prompt list changed"), nil
		})

	// These answer plainly: the scenarios check that a request without
	// _meta logLevel gets no notifications/message, and that a 2026-07-28
	// response stream carries no independent JSON-RPC requests (the library
	// sends none there; elicitation uses InputRequiredResult).
	s.RegisterTool(mcp.NewTool("test_logging_tool", "Runs without logging unless asked to"),
		text("Logging tool completed"))
	s.RegisterTool(mcp.NewTool("test_streaming_elicitation", "Answers on its response stream"),
		text("Streaming tool completed"))

	// x-mcp-header: each annotated argument is mirrored in an
	// Mcp-Param-{Name} header, which the server checks against the body.
	s.RegisterTool(mcp.NewTool("test_headers", "A tool with x-mcp-header parameters",
		mcp.String("region", "Region", mcp.Required(), mcp.HTTPHeader("Region")),
		mcp.Integer("priority", "Priority", mcp.HTTPHeader("Priority")),
		mcp.Boolean("verbose", "Verbose", mcp.HTTPHeader("Verbose")),
		mcp.String("query", "Query", mcp.Required()),
	), text("Headers matched"))
}

// text answers every call with the same text.
func text(t string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText(t), nil
	}
}

func registerResources(s *mcp.Server) {
	s.RegisterResource(mcp.NewResource("test://static-text", "Static text", "A static text resource", "text/plain"),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			return mcp.NewResourceResponseText(req.URI(), "This is the content of the static text resource.", "text/plain"), nil
		})
	s.RegisterResource(mcp.NewResource("test://static-binary", "Static binary", "A static binary resource", "image/png"),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			return mcp.NewResourceResponseBlob(req.URI(), redPixelPNG, "image/png"), nil
		})
	s.RegisterResource(mcp.NewResource("test://watched-resource", "Watched resource", "A resource clients may subscribe to", "text/plain"),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			return mcp.NewResourceResponseText(req.URI(), "Watched resource content.", "text/plain"), nil
		})
	// The id completer gives completion/complete a resource template to
	// complete as well as a prompt.
	s.RegisterResourceTemplate(mcp.NewResourceTemplate("test://template/{id}/data", "Template data", "Data for an id", "application/json").
		Complete("id", func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
			return mcp.FilterPrefix([]string{"1", "2", "3", "42", "100"}, req.Value()), nil
		}),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			id := req.StringOr("id", "")
			body := fmt.Sprintf(`{"id":%q,"templateTest":true,"data":%q}`, id, "Data for ID: "+id)
			return mcp.NewResourceResponseText(req.URI(), body, "application/json"), nil
		})
}

func registerPrompts(s *mcp.Server) {
	s.RegisterPrompt(mcp.NewPrompt("test_simple_prompt", "A prompt without arguments"),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			return mcp.NewPromptResponseText("This is a simple prompt for testing."), nil
		})

	// completion-complete asks for arg1 completions; arg2's completer
	// shows context.arguments carrying the arg1 already chosen.
	s.RegisterPrompt(mcp.NewPrompt("test_prompt_with_arguments", "A prompt with two required arguments").
		Argument("arg1", "First argument", true).
		Complete(func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
			return mcp.FilterPrefix([]string{"paris", "park", "party", "test", "testing"}, req.Value()), nil
		}).
		Argument("arg2", "Second argument", true).
		Complete(func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
			arg1, _ := req.ContextArg("arg1")
			return mcp.FilterPrefix([]string{arg1 + "-one", arg1 + "-two"}, req.Value()), nil
		}),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			return mcp.NewPromptResponseText(fmt.Sprintf("Prompt with arguments: arg1='%s', arg2='%s'", req.StringOr("arg1", ""), req.StringOr("arg2", ""))), nil
		})

	s.RegisterPrompt(mcp.NewPrompt("test_prompt_with_embedded_resource", "A prompt that embeds a resource").
		Argument("resourceUri", "URI of the resource to embed", true),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			return mcp.NewPromptResponseMessages(
				mcp.PromptMessage{Role: mcp.PromptRoleUser, Content: mcp.PromptMessageContent{
					Type:     "resource",
					Resource: &mcp.ResourceContent{URI: req.StringOr("resourceUri", ""), MimeType: "text/plain", Text: "Embedded resource content for testing."},
				}},
				mcp.NewPromptTextMessage(mcp.PromptRoleUser, "Please process the embedded resource above."),
			), nil
		})

	s.RegisterPrompt(mcp.NewPrompt("test_prompt_with_image", "A prompt with an image"),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			image := mcp.NewToolResponseImage(redPixelPNG, "image/png").Content[0]
			return mcp.NewPromptResponseMessages(
				mcp.PromptMessage{Role: mcp.PromptRoleUser, Content: image},
				mcp.NewPromptTextMessage(mcp.PromptRoleUser, "Please analyze the image above."),
			), nil
		})
}

// schemaProvider serves json_schema_2020_12_tool, whose inputSchema uses
// JSON Schema 2020-12 keywords ($schema, $defs, $ref, $anchor, allOf,
// anyOf, if/then/else): a ToolProvider passes a tool's schema through as
// written, which the suite checks is not stripped, and the server
// validates calls against it.
type schemaProvider struct{}

func (schemaProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
	return []mcp.MCPTool{{
		Name:        "json_schema_2020_12_tool",
		Description: "A tool whose input schema uses JSON Schema 2020-12 features",
		InputSchema: map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "object",
			"$defs": map[string]any{
				"address": map[string]any{
					"$anchor": "address",
					"type":    "object",
					"properties": map[string]any{
						"street": map[string]any{"type": "string"},
						"city":   map[string]any{"type": "string"},
					},
				},
			},
			"properties": map[string]any{
				"name":    map[string]any{"type": "string"},
				"address": map[string]any{"$ref": "#/$defs/address"},
				"contact": map[string]any{"anyOf": []any{
					map[string]any{"type": "string", "format": "email"},
					map[string]any{"type": "string", "pattern": "^[0-9+ -]+$"},
				}},
				"kind": map[string]any{"type": "string", "enum": []any{"person", "company"}},
				"vat":  map[string]any{"type": "string"},
			},
			"allOf": []any{
				map[string]any{"required": []any{"name"}},
				map[string]any{"anyOf": []any{
					map[string]any{"required": []any{"address"}},
					map[string]any{"required": []any{"contact"}},
				}},
			},
			"if":                   map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "company"}}, "required": []any{"kind"}},
			"then":                 map[string]any{"required": []any{"vat"}},
			"else":                 map[string]any{"not": map[string]any{"required": []any{"vat"}}},
			"additionalProperties": false,
		},
		Visibility: mcp.ToolVisibilityNative,
	}}, nil
}

func (schemaProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*mcp.ToolResponse, error) {
	return mcp.NewToolResponseJSON(params), nil
}

// redPixelPNG is a 1x1 red PNG.
var redPixelPNG = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

// silentWAV is a short 8 kHz, 8-bit mono WAV of silence.
var silentWAV = func() []byte {
	samples := bytes.Repeat([]byte{128}, 800)
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(samples)))
	buf.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(8000), uint32(8000), uint16(1), uint16(8)} {
		_ = binary.Write(&buf, binary.LittleEndian, v)
	}
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(samples)))
	buf.Write(samples)
	return buf.Bytes()
}()
