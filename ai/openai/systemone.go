package openai

// System One (decision model) types, shared by the providers that support
// them (Ollama's POST /v1/systemone). They live here — the shared types
// package — because provider packages cannot import the parent ai package
// (ai's factory imports them).
//
// A decision request scores a state against 1–64 named questions in one
// non-streaming response; it is not a chat completion.

// SystemOneRequest is the body of a System One decision request.
type SystemOneRequest struct {
	// Model is the decision model to run (e.g. "clef-flash", "nimble").
	Model string `json:"model"`
	// State is the input the questions are judged against: a nonempty
	// string, or a JSON-serializable object or array.
	State any `json:"state"`
	// Images are base64-encoded images shared by all questions. Requires a
	// vision-capable decision model (Clef, Clef Flash).
	Images []string `json:"images,omitempty"`
	// Questions maps a name to the question asked about State. 1–64 entries.
	Questions map[string]SystemOneQuestion `json:"questions"`
	// KeepAlive is Ollama's model keep-alive: a duration string ("5m"), a
	// number of seconds (0 unloads, negative keeps loaded), or nil for the
	// server default.
	KeepAlive any `json:"keep_alive,omitempty"`
}

// SystemOneQuestion is one named question about the state.
//
// Criteria's shape depends on Type:
//   - "choice": a map of 2–26 option name → description (values may be null)
//   - "noul":   optional; a map with "false"/"true" descriptions
//   - "score":  an ordered list of 2–26 descriptions, lowest to highest
type SystemOneQuestion struct {
	// Type is "choice", "noul" (yes/no probability) or "score".
	Type string `json:"type"`
	// Instructions tells the model what to judge.
	Instructions string `json:"instructions"`
	// Criteria is the choice options, noul labels or ordered score rubric.
	Criteria any `json:"criteria,omitempty"`
}

// SystemOneAnswer is one named answer. Which fields are set depends on the
// question type; Probabilities and Confidence are present for choice and
// score, Confidence for noul.
type SystemOneAnswer struct {
	Type string `json:"type"`
	// Choice is the top-probability option name (choice questions).
	Choice string `json:"choice,omitempty"`
	// Noul is the probability of true, 0–1 (noul questions).
	Noul *float64 `json:"noul,omitempty"`
	// Score is the probability-weighted average of the zero-based rubric
	// levels, 0 to len(rubric)-1 (score questions).
	Score *float64 `json:"score,omitempty"`
	// Legend repeats the ordered rubric descriptions, keyed by the same
	// zero-based level-index strings as Probabilities (score questions).
	Legend map[string]string `json:"legend,omitempty"`
	// Probabilities maps option name (choice) or level index as a string
	// (score) to its probability.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	// Confidence is 1 − H(p)/ln(N): 0 means uniform, near 1 one dominant
	// candidate. High confidence does not guarantee correctness.
	Confidence float64 `json:"confidence,omitempty"`
}

// SystemOneUsage reports token usage. Output tokens are internal scoring
// tokens, not JSON length.
type SystemOneUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneResponse is the single JSON response of a decision request.
type SystemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]SystemOneAnswer `json:"answers"`
	Usage   SystemOneUsage             `json:"usage"`
}
