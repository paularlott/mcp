package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/paularlott/mcp"
)

// registerElicitation adds the fixtures the elicitation scenarios call. Each
// uses mcp.Elicit, which sends elicitation/create to a 2025-06-18/2025-11-25
// client and answers a 2026-07-28 request with an InputRequiredResult; the
// same handler serves every revision.
func registerElicitation(s *mcp.Server) {
	// 2025-x: tools-call-elicitation.
	s.RegisterTool(mcp.NewTool("test_elicitation", "Asks the user for a username and email",
		mcp.String("message", "The message to show the user", mcp.Required()),
	), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		res, err := mcp.Elicit(ctx, mcp.ElicitForm{
			Message: req.StringOr("message", ""),
			Schema: object(map[string]any{
				"username": map[string]any{"type": "string", "description": "User's response"},
				"email":    map[string]any{"type": "string", "description": "User's email address"},
			}, "username", "email"),
		})
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResponseText("User response: " + describe(res)), nil
	})

	// 2025-11-25: elicitation-sep1034-defaults (a default for every primitive type).
	s.RegisterTool(mcp.NewTool("test_elicitation_sep1034_defaults", "Elicits with default values"),
		elicitTool("Please review your details", object(map[string]any{
			"name":     map[string]any{"type": "string", "default": "John Doe"},
			"age":      map[string]any{"type": "integer", "default": 30},
			"score":    map[string]any{"type": "number", "default": 95.5},
			"status":   map[string]any{"type": "string", "enum": []string{"active", "inactive", "pending"}, "default": "active"},
			"verified": map[string]any{"type": "boolean", "default": true},
		})))

	// 2025-11-25: elicitation-sep1330-enums (all five enum shapes).
	s.RegisterTool(mcp.NewTool("test_elicitation_sep1330_enums", "Elicits with every enum variant"),
		elicitTool("Please choose your options", object(map[string]any{
			"untitledSingle": map[string]any{"type": "string", "enum": []string{"option1", "option2", "option3"}},
			"titledSingle": map[string]any{"type": "string", "oneOf": []any{
				map[string]any{"const": "value1", "title": "First Option"},
				map[string]any{"const": "value2", "title": "Second Option"},
				map[string]any{"const": "value3", "title": "Third Option"},
			}},
			"legacyEnum": map[string]any{"type": "string", "enum": []string{"opt1", "opt2", "opt3"},
				"enumNames": []string{"Option One", "Option Two", "Option Three"}},
			"untitledMulti": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"option1", "option2", "option3"}}},
			"titledMulti": map[string]any{"type": "array", "items": map[string]any{"anyOf": []any{
				map[string]any{"const": "value1", "title": "First Choice"},
				map[string]any{"const": "value2", "title": "Second Choice"},
				map[string]any{"const": "value3", "title": "Third Choice"},
			}}},
		})))

	// 2026-07-28: the input-required-result-* scenarios.
	nameSchema := object(map[string]any{"name": map[string]any{"type": "string"}}, "name")
	confirmSchema := object(map[string]any{"ok": map[string]any{"type": "boolean"}}, "ok")

	s.RegisterTool(mcp.NewTool("test_input_required_result_elicitation", "Asks for the user's name"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			res, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "user_name", Message: "What is your name?", Schema: nameSchema})
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResponseText(fmt.Sprintf("Hello, %v!", res.Content["name"])), nil
		})

	// requestState is verified by the library before the handler runs, so
	// reaching the second Elicit answer means the state checked out.
	confirm := func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		res, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "confirm", Message: "Please confirm", Schema: confirmSchema})
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResponseText("state-ok: " + describe(res)), nil
	}
	s.RegisterTool(mcp.NewTool("test_input_required_result_request_state", "Asks for confirmation"), confirm)
	s.RegisterTool(mcp.NewTool("test_input_required_result_tampered_state", "Asks for confirmation"), confirm)

	// Two rounds: step1's answer travels in requestState while step2 is asked.
	s.RegisterTool(mcp.NewTool("test_input_required_result_multi_round", "Asks two questions in turn"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "step1", Message: "Step 1: What is your name?", Schema: nameSchema})
			if err != nil {
				return nil, err
			}
			color, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "step2", Message: "Step 2: What is your favorite color?",
				Schema: object(map[string]any{"color": map[string]any{"type": "string"}}, "color")})
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResponseText(fmt.Sprintf("%v likes %v", name.Content["name"], color.Content["color"])), nil
		})

	// Elicitation only: the scenario also wants sampling and roots/list
	// input requests, which the library deliberately does not implement.
	s.RegisterTool(mcp.NewTool("test_input_required_result_multiple_inputs", "Asks two questions at once"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			res, err := mcp.ElicitAll(ctx,
				mcp.ElicitForm{Key: "user_name", Message: "What is your name?", Schema: nameSchema},
				mcp.ElicitForm{Key: "confirm", Message: "Please confirm", Schema: confirmSchema})
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResponseText(describe(res[0]) + "; " + describe(res[1])), nil
		})

	// Asks only when the request's clientCapabilities declare elicitation.
	s.RegisterTool(mcp.NewTool("test_input_required_result_capabilities", "Elicits only from capable clients"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			res, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "user_name", Message: "What is your name?", Schema: nameSchema})
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResponseText(describe(res)), nil
		})

	// input-required-result-non-tool-request: prompts/get.
	s.RegisterPrompt(mcp.NewPrompt("test_input_required_result_prompt", "Asks for context before rendering"),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			res, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "user_context", Message: "What context should the prompt use?",
				Schema: object(map[string]any{"context": map[string]any{"type": "string"}}, "context")})
			if err != nil {
				return nil, err
			}
			return mcp.NewPromptResponseText(fmt.Sprintf("Use this context: %v", res.Content["context"])), nil
		})
}

// elicitTool asks one form and reports the outcome.
func elicitTool(message string, schema map[string]any) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		res, err := mcp.Elicit(ctx, mcp.ElicitForm{Message: message, Schema: schema})
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResponseText("Elicitation completed: " + describe(res)), nil
	}
}

func object(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func describe(res *mcp.ElicitResult) string {
	content, _ := json.Marshal(res.Content)
	return fmt.Sprintf("action=%s, content=%s", res.Action, content)
}
