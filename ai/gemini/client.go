package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/mcp/pool"
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	providerName   = "gemini"
)

type Client struct {
	apiKey     string
	baseURL    string
	httpPool   pool.HTTPPool
	provider   string
	chatClient *openai.Client // Use OpenAI client for chat/streaming
	responseManager *openai.ResponseManager
}

func New(config openai.Config) (*Client, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if !strings.HasSuffix(config.BaseURL, "/") {
		config.BaseURL += "/"
	}

	// Create OpenAI client for chat/streaming using /openai/ endpoint
	chatClient, err := openai.New(openai.Config{
		APIKey:              config.APIKey,
		BaseURL:             config.BaseURL + "openai/",
		ExtraHeaders:        config.ExtraHeaders,
		HTTPPool:            config.HTTPPool,
		LocalServer:         config.LocalServer,
		RemoteServerConfigs: config.RemoteServerConfigs,
		MaxTokens:           config.MaxTokens,
		Temperature:         config.Temperature,
		TopP:                config.TopP,
		FrequencyPenalty:    config.FrequencyPenalty,
		PresencePenalty:     config.PresencePenalty,
		RequestTimeout:      config.RequestTimeout,
	})
	if err != nil {
		return nil, err
	}

	// Get the global response manager
	responseManager := openai.GetManager()

	return &Client{
		apiKey:          config.APIKey,
		baseURL:         config.BaseURL,
		httpPool:        config.HTTPPool,
		provider:        providerName,
		chatClient:      chatClient,
		responseManager: responseManager,
	}, nil
}

// Provider returns the provider name
func (c *Client) Provider() string {
	return c.provider
}

// SupportsCapability checks if the provider supports a capability
func (c *Client) SupportsCapability(cap string) bool {
	// Gemini supports embeddings via custom API, but not responses API
	return cap != "responses"
}

// ChatCompletion delegates to OpenAI client (uses /openai/ endpoint)
func (c *Client) ChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	return c.chatClient.ChatCompletion(ctx, req)
}

// StreamChatCompletion delegates to OpenAI client (uses /openai/ endpoint)
func (c *Client) StreamChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) *openai.ChatStream {
	return c.chatClient.StreamChatCompletion(ctx, req)
}

// GetModels fetches the list of available models from Gemini API
func (c *Client) GetModels(ctx context.Context) (*openai.ModelsResponse, error) {
	type geminiModelResponse struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	var geminiResp geminiModelResponse
	endpoint := fmt.Sprintf("models?key=%s", c.apiKey)
	if err := c.doRequest(ctx, "GET", endpoint, nil, &geminiResp); err != nil {
		return nil, err
	}

	models := make([]openai.Model, 0, len(geminiResp.Models))
	for _, m := range geminiResp.Models {
		modelID := m.Name
		modelID = strings.TrimPrefix(modelID, "models/")
		models = append(models, openai.Model{
			ID:     modelID,
			Object: "model",
		})
	}

	return &openai.ModelsResponse{
		Object: "list",
		Data:   models,
	}, nil
}

// CreateEmbedding creates embeddings using Gemini's embedContent API
func (c *Client) CreateEmbedding(ctx context.Context, req openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	// Convert input to string or array of strings
	var content []string
	switch v := req.Input.(type) {
	case string:
		content = []string{v}
	case []string:
		content = v
	case []interface{}:
		content = make([]string, len(v))
		for i, item := range v {
			if s, ok := item.(string); ok {
				content[i] = s
			} else {
				return nil, fmt.Errorf("invalid input type")
			}
		}
	default:
		return nil, fmt.Errorf("invalid input type")
	}

	embeddings := make([]openai.Embedding, len(content))
	for i, text := range content {
		geminiReq := map[string]interface{}{
			"content": map[string]interface{}{
				"parts": []map[string]string{{"text": text}},
			},
			"taskType": "SEMANTIC_SIMILARITY",
		}
		if req.Dimensions > 0 {
			geminiReq["outputDimensionality"] = req.Dimensions
		}

		var geminiResp struct {
			Embedding struct {
				Values []float64 `json:"values"`
			} `json:"embedding"`
		}

		endpoint := fmt.Sprintf("models/%s:embedContent?key=%s", req.Model, c.apiKey)
		if err := c.doRequest(ctx, "POST", endpoint, geminiReq, &geminiResp); err != nil {
			return nil, err
		}

		embeddings[i] = openai.Embedding{
			Object:    "embedding",
			Embedding: geminiResp.Embedding.Values,
			Index:     i,
		}
	}

	return &openai.EmbeddingResponse{
		Object: "list",
		Data:   embeddings,
		Model:  req.Model,
		Usage: openai.Usage{
			PromptTokens: len(content),
			TotalTokens:  len(content),
		},
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = strings.NewReader(string(data))
	}

	var httpClient *http.Client
	if c.httpPool != nil {
		httpClient = c.httpPool.GetHTTPClient()
	} else {
		httpClient = pool.GetPool().GetHTTPClient()
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gemini API error: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// StreamResponse emulates the OpenAI Responses API streaming using chat completions
func (c *Client) StreamResponse(ctx context.Context, req openai.CreateResponseRequest) *openai.ResponseStream {
	eventChan := make(chan openai.ResponseStreamEvent, 50)
	errorChan := make(chan error, 1)
	go func() {
		defer close(eventChan)
		defer close(errorChan)
		openai.StreamResponseEmulated(ctx, c.chatClient, req, eventChan, errorChan)
	}()
	return openai.NewResponseStream(ctx, eventChan, errorChan)
}

// CreateResponse emulates the OpenAI Responses API using chat completions
// Processes async in the background and returns immediately with an in_progress status
func (c *Client) CreateResponse(ctx context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	return openai.CreateResponseEmulated(ctx, c, c.responseManager, req)
}

// GetResponse retrieves a response by ID (blocking until complete or error)
func (c *Client) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.GetResponseEmulated(ctx, c.responseManager, id)
}

// CancelResponse cancels an in-progress response
func (c *Client) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.CancelResponseEmulated(ctx, c.responseManager, id)
}

// DeleteResponse deletes a response by ID
func (c *Client) DeleteResponse(ctx context.Context, id string) error {
	return openai.DeleteResponseEmulated(ctx, c.responseManager, id)
}

// CompactResponse compacts a response by removing intermediate reasoning steps
func (c *Client) CompactResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.CompactResponseEmulated(ctx, c.responseManager, id)
}

// Close closes the client
// Note: Response managers persist for 15 minutes after last response expires
func (c *Client) Close() error {
	return nil
}
