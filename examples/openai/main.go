package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
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
	fmt.Println("  POST /v1/chat/completions (supports streaming with tool status events)")
	fmt.Printf("Upstream LLM: %s (model: %s)\n", openAIBaseURL, defaultModel)

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	if req.Stream {
		handleStreamingChatCompletions(w, r, req, mcpServer)
	} else {
		handleNonStreamingChatCompletions(w, r, req, mcpServer)
	}
}

// handleNonStreamingChatCompletions handles non-streaming chat completions
func handleNonStreamingChatCompletions(w http.ResponseWriter, r *http.Request, req openai.ChatCompletionRequest, mcpServer *mcp.Server) {
	const maxIterations = 10
	currentMessages := req.Messages

	// Create a tool executor that calls MCP tools
	executor := func(name string, arguments map[string]any) (string, error) {
		log.Printf("Executing tool: %s", name)
		mcpResponse, err := mcpServer.CallTool(r.Context(), name, arguments)
		if err != nil {
			return "", err
		}
		result, _ := openai.ExtractToolResult(mcpResponse)
		log.Printf("Tool result: %s", result)
		return result, nil
	}

	for iteration := 0; iteration < maxIterations; iteration++ {
		req.Messages = currentMessages

		response, err := callUpstreamLLM(r.Context(), req)
		if err != nil {
			http.Error(w, fmt.Sprintf("LLM error: %v", err), http.StatusInternalServerError)
			return
		}

		if len(response.Choices) == 0 || !openai.HasToolCalls(response.Choices[0].Message) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		// Add assistant message with tool calls
		currentMessages = append(currentMessages,
			openai.BuildAssistantToolCallMessage(
				response.Choices[0].Message.GetContentAsString(),
				response.Choices[0].Message.ToolCalls,
			))

		// Execute all tool calls and get result messages
		toolResultMsgs, err := openai.ExecuteToolCalls(
			response.Choices[0].Message.ToolCalls,
			executor,
			false, // continue on error
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("Tool execution error: %v", err), http.StatusInternalServerError)
			return
		}

		currentMessages = append(currentMessages, toolResultMsgs...)
	}

	http.Error(w, openai.NewMaxToolIterationsError(maxIterations).Error(), http.StatusInternalServerError)
}

// handleStreamingChatCompletions handles streaming chat completions with tool status events
func handleStreamingChatCompletions(w http.ResponseWriter, r *http.Request, req openai.ChatCompletionRequest, mcpServer *mcp.Server) {
	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Create SSE writer and tool handler for sending tool status events
	sseWriter := openai.NewSimpleSSEWriter(w, flusher.Flush)
	toolHandler := openai.NewSSEToolHandler(sseWriter, func(err error, eventType, toolName string) {
		log.Printf("Failed to write %s event for %s: %v", eventType, toolName, err)
	})

	const maxIterations = 10
	currentMessages := req.Messages

	for iteration := 0; iteration < maxIterations; iteration++ {
		req.Messages = currentMessages
		req.Stream = true

		// Stream from upstream LLM and accumulate the response
		accumulator := &openai.CompletionAccumulator{}
		err := streamFromUpstreamLLM(r.Context(), req, func(chunk openai.ChatCompletionResponse) error {
			accumulator.AddChunk(chunk)

			// Check if this chunk has tool calls - if so, don't forward to client
			// (we'll process tools server-side and continue the conversation)
			hasToolCalls := len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta.ToolCalls) > 0
			isToolCallsFinish := len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason == "tool_calls"

			if !hasToolCalls && !isToolCallsFinish {
				// Forward non-tool chunks to client
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}

			return nil
		})

		if err != nil {
			log.Printf("Streaming error: %v", err)
			return
		}

		// Check if we have tool calls to process
		toolCalls, hasTools := accumulator.FinishedToolCalls()
		if !hasTools {
			// No tool calls - we're done
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

		// Add assistant message with tool calls to conversation
		currentMessages = append(currentMessages,
			openai.BuildAssistantToolCallMessage(
				accumulator.Content(),
				toolCalls,
			))

		// Execute tools with status events
		for _, tc := range toolCalls {
			// Send tool_start event
			toolHandler.OnToolCall(tc)

			// Execute the tool
			mcpResponse, err := mcpServer.CallTool(r.Context(), tc.Function.Name, tc.Function.Arguments)
			var result string
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
				log.Printf("Tool %s error: %v", tc.Function.Name, err)
			} else {
				result, _ = openai.ExtractToolResult(mcpResponse)
				log.Printf("Tool %s result: %s", tc.Function.Name, result)
			}

			// Send tool_end event
			toolHandler.OnToolResult(tc.ID, tc.Function.Name, result)

			// Add tool result to messages
			currentMessages = append(currentMessages, openai.BuildToolResultMessage(tc.ID, result))
		}
	}

	// Max iterations reached
	log.Printf("Max tool iterations (%d) reached", maxIterations)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func callUpstreamLLM(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	req.Stream = false // Ensure non-streaming
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

// streamFromUpstreamLLM streams a chat completion from the upstream LLM
func streamFromUpstreamLLM(ctx context.Context, req openai.ChatCompletionRequest, onChunk func(openai.ChatCompletionResponse) error) error {
	req.Stream = true
	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		openAIBaseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+openAIAPIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream error (%d): %s", resp.StatusCode, string(body))
	}

	// Parse SSE stream
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Parse data lines
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			// Check for end of stream
			if data == "[DONE]" {
				break
			}

			var chunk openai.ChatCompletionResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				log.Printf("Failed to parse chunk: %v", err)
				continue
			}

			if err := onChunk(chunk); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
}
