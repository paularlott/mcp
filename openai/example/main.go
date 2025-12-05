package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/openai"
)

var (
	openAIBaseURL = getEnv("OPENAI_BASE_URL", "http://127.0.0.1:1234/v1")
	openAIAPIKey  = getEnv("OPENAI_API_KEY", "lm-studio")
	defaultModel  = getEnv("DEFAULT_MODEL", "qwen/qwen3-1.7b")
)

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func main() {
	mcpServer := mcp.NewServer("greeting-server", "1.0.0")

	mcpServer.RegisterTool(
		mcp.NewTool("greet", "Greet someone with a friendly message from MCP",
			mcp.String("name", "The name of the person to greet", mcp.Required()),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, _ := req.String("name")
			return mcp.NewToolResponseText(fmt.Sprintf("Hi %s! Greetings from MCP!", name)), nil
		},
	)

	http.HandleFunc("/v1/models", handleModels)
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		handleChatCompletions(w, r, mcpServer)
	})
	http.HandleFunc("/mcp", mcpServer.HandleRequest)

	fmt.Println("OpenAI-compatible server starting on :8080")
	fmt.Println("  GET  /v1/models")
	fmt.Println("  POST /v1/chat/completions")
	fmt.Printf("Upstream LLM: %s (model: %s)\n", openAIBaseURL, defaultModel)

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Forward to upstream LLM
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		openAIBaseURL+"/models", nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
		return
	}

	httpReq.Header.Set("Authorization", "Bearer "+openAIAPIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch models: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request, mcpServer *mcp.Server) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req openai.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Model == "" {
		req.Model = defaultModel
	}

	mcpTools := mcpServer.ListTools()
	req.Tools = append(req.Tools, openai.MCPToolsToOpenAI(mcpTools)...)

	const maxIterations = 10
	currentMessages := req.Messages

	for iteration := 0; iteration < maxIterations; iteration++ {
		req.Messages = currentMessages

		response, err := callUpstreamLLM(r.Context(), req)
		if err != nil {
			http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
			return
		}

		if len(response.Choices) == 0 || len(response.Choices[0].Message.ToolCalls) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		currentMessages = append(currentMessages, response.Choices[0].Message)

		for _, toolCall := range response.Choices[0].Message.ToolCalls {
			log.Printf("Executing tool: %s", toolCall.Function.Name)

			mcpResponse, err := mcpServer.CallTool(r.Context(), toolCall.Function.Name, toolCall.Function.Arguments)

			var resultContent string
			if err != nil {
				resultContent = fmt.Sprintf("Error: %v", err)
			} else {
				resultContent, _ = openai.ExtractToolResult(mcpResponse)
			}

			log.Printf("Tool result: %s", resultContent)

			toolResultMsg := openai.Message{
				Role:       "tool",
				ToolCallID: toolCall.ID,
			}
			toolResultMsg.SetContentAsString(resultContent)
			currentMessages = append(currentMessages, toolResultMsg)
		}
	}

	http.Error(w, "Maximum tool iterations reached", http.StatusInternalServerError)
}

func callUpstreamLLM(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		openAIBaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+openAIAPIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream error (%d): %s", resp.StatusCode, string(body))
	}

	var response openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}
