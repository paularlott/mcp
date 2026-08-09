package ollama

// Native Ollama API wire types. Ollama is not OpenAI-compatible on this
// surface — these shapes match https://github.com/ollama/ollama/blob/main/docs/api.md.
// The client (client.go) translates between these and the OpenAI-shaped
// ai.Client interface, the same way the claude and gemini packages do.

// message is an Ollama chat message.
type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Images    []string   `json:"images,omitempty"` // base64-encoded, no data: prefix
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

// toolCall is an assistant-requested tool call. Unlike OpenAI, Ollama carries
// arguments as a JSON object, not a JSON string.
type toolCall struct {
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// tool is a tool definition exposed to the model.
type tool struct {
	Type     string         `json:"type"` // always "function"
	Function toolDefinition `json:"function"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// options carries Ollama model runtime options (temperature, top_p, num_predict,
// num_ctx, ...). Sent as the top-level "options" object on chat/generate.
type options map[string]any

// chatRequest is the body for POST /api/chat.
type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []message `json:"messages"`
	Tools     []tool    `json:"tools,omitempty"`
	Stream    bool      `json:"stream"`
	Options   options   `json:"options,omitempty"`
	Format    any       `json:"format,omitempty"` // "json", or a JSON schema object
	KeepAlive *string   `json:"keep_alive,omitempty"`
}

// chatResponse is one object in the /api/chat response — the whole response
// when not streaming, or one newline-delimited object when streaming. The
// terminal stream object has Done == true and carries the usage counts.
type chatResponse struct {
	Model           string  `json:"model"`
	CreatedAt       string  `json:"created_at"`
	Message         message `json:"message"`
	DoneReason      string  `json:"done_reason,omitempty"`
	Done            bool    `json:"done"`
	PromptEvalCount int     `json:"prompt_eval_count,omitempty"`
	EvalCount       int     `json:"eval_count,omitempty"`
	TotalDuration   int64   `json:"total_duration,omitempty"`
}

// embedRequest is the body for POST /api/embed (Ollama >= 0.1.34; supersedes
// /api/embeddings). Input is a list of strings.
type embedRequest struct {
	Model    string    `json:"model"`
	Input    []string  `json:"input"`
	Truncate *bool     `json:"truncate,omitempty"`
	Options  options   `json:"options,omitempty"`
}

type embedResponse struct {
	Model          string      `json:"model"`
	Embeddings     [][]float64 `json:"embeddings"`
	TotalDuration  int64       `json:"total_duration,omitempty"`
	LoadDuration   int64       `json:"load_duration,omitempty"`
	PromptEvalCount int        `json:"prompt_eval_count,omitempty"`
}

// tagsResponse is the body for GET /api/tags (list installed models).
type tagsResponse struct {
	Models []tagModel `json:"models"`
}

type tagModel struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	ModifiedAt string `json:"modified_at"`
	Details   struct {
		Family           string `json:"family"`
		Families         []string `json:"families"`
		ParameterSize    string `json:"parameter_size"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
}

// showRequest is the body for POST /api/show (model details).
type showRequest struct {
	Name string `json:"name"`
}

// showResponse carries the fields we read from /api/show. model_info is the
// raw GGUF metadata; the context length lives under a family-qualified key
// (e.g. llama.context_length) or a bare context_length.
type showResponse struct {
	Modelfile  string         `json:"modelfile"`
	Parameters string         `json:"parameters"`
	ModelInfo  map[string]any `json:"model_info"`
	Details    struct {
		Family   string   `json:"family"`
		Families []string `json:"families"`
	} `json:"details"`
}
