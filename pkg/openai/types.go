package openai

// Model represents an available model.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	// Type is a relai extension to the OpenAI schema: "chat", "embedding", or
	// "rerank", so clients can tell what a model/router is for.
	Type string `json:"type,omitempty"`
	// ContextLength is a relai extension: the context window the serving
	// backend advertises (probed via /health or /props). Omitted when no
	// backend reports one, so strict OpenAI clients never see a fabricated 0.
	ContextLength int `json:"context_length,omitempty"`
}

// ModelList represents a list of models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// ChatCompletionRequest is the request body for chat completions.
type ChatCompletionRequest struct {
	Model            string          `json:"model"`
	Messages         []ChatMessage   `json:"messages"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	N                *int            `json:"n,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	Stop             interface{}     `json:"stop,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	Functions        []Function      `json:"functions,omitempty"`
	Tools            []Tool          `json:"tools,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	LogitBias        map[string]int  `json:"logit_bias,omitempty"`
	User             string          `json:"user,omitempty"`
}

// ChatMessage represents a message in a chat.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// Function represents a function definition.
type Function struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  JSON   `json:"parameters,omitempty"`
}

// Tool represents a tool definition.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// ResponseFormat specifies the response format.
type ResponseFormat struct {
	Type string `json:"type"`
}

// JSON is a flexible JSON type.
type JSON map[string]interface{}

// EmbeddingRequest is the request body for embeddings.
type EmbeddingRequest struct {
	Model          string      `json:"model"`
	Input          interface{} `json:"input"` // string, []string, []int, or [][]int
	User           string      `json:"user,omitempty"`
	EncodingFormat string      `json:"encoding_format,omitempty"`
	Dimensions     *int        `json:"dimensions,omitempty"`
}

// RerankRequest is the request body for reranking (Cohere/Jina-compatible).
type RerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            *int     `json:"top_n,omitempty"`
	ReturnDocuments *bool    `json:"return_documents,omitempty"`
	MaxChunksPerDoc *int     `json:"max_chunks_per_doc,omitempty"`
}

// CompletionRequest is the request body for legacy completions.
type CompletionRequest struct {
	Model            string   `json:"model"`
	Prompt           string   `json:"prompt"`
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	Stream           bool     `json:"stream,omitempty"`
	MaxTokens        *int     `json:"max_tokens,omitempty"`
	Stop             string   `json:"stop,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
}
