package deepseek

import "encoding/json"

// Ptr preserves explicit zero/false values in optional request parameters.
func Ptr[T any](v T) *T {
	return &v
}

// ChatCompletionRequest configures a completion. Optional pointers preserve explicit zero values.
type ChatCompletionRequest struct {
	Model            string          `json:"model"`
	Messages         []Message       `json:"messages"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	Thinking         *Thinking       `json:"thinking,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	ResponseFormat   *ResponseFormat `json:"response_format,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	Tools            []Tool          `json:"tools,omitempty"`
	ToolChoice       *ToolChoice     `json:"tool_choice,omitempty"`
	Logprobs         *bool           `json:"logprobs,omitempty"`
	TopLogprobs      *int            `json:"top_logprobs,omitempty"`
	Stream           bool            `json:"stream"` // Set by the chosen client method.
	StreamOptions    *StreamOptions  `json:"stream_options,omitempty"`
}

// Thinking selects the server thinking mode (enabled or disabled).
type Thinking struct {
	Type string `json:"type"`
}

// ResponseFormat selects text or json_object output.
type ResponseFormat struct {
	Type string `json:"type"`
}

// StreamOptions controls usage reporting on streaming requests.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Message is a conversation message. Use Text or Parts to construct Content.
type Message struct {
	Role             string     `json:"role"`
	Content          Content    `json:"content"`
	Name             string     `json:"name,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	Prefix           bool       `json:"prefix,omitempty"`
}

// ContentPart is a text, image_url, or file input part. Set fields for its Type only.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	FileID   string    `json:"file_id,omitempty"`
	FileData string    `json:"file_data,omitempty"`
	Filename string    `json:"filename,omitempty"`
}

// ImageURL describes an image URL or supported inline data URL.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Tool declares a function the model may request; the SDK never executes it.
type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition contains a function name and its JSON Schema parameters.
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ToolCall describes a call or a streaming delta. Index is streaming-only.
type ToolCall struct {
	Index    *int         `json:"index,omitempty"` // Present on streaming deltas, including zero.
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// FunctionCall contains a name and JSON arguments; deltas may contain partial JSON.
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// ChatCompletion contains the complete response and HTTP metadata.
type ChatCompletion struct {
	ResponseMetadata  `json:"-"`
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage"`
}

// Choice contains one complete assistant response.
type Choice struct {
	Index        int             `json:"index"`
	Message      Message         `json:"message"`
	FinishReason *string         `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs"`
}

// ChatCompletionChunk contains incremental choices and optional usage.
type ChatCompletionChunk struct {
	ID                string        `json:"id"`
	Object            string        `json:"object"`
	Created           int64         `json:"created"`
	Model             string        `json:"model"`
	SystemFingerprint string        `json:"system_fingerprint"`
	Choices           []ChunkChoice `json:"choices"`
	Usage             *Usage        `json:"usage"`
}

// ChunkChoice contains an indexed delta and an optional termination reason.
type ChunkChoice struct {
	Index        int             `json:"index"`
	Delta        Delta           `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs"`
}

// Delta contains incremental text, reasoning, and tool-call fields.
type Delta struct {
	Role             string     `json:"role,omitempty"`
	Content          *string    `json:"content"`
	ReasoningContent *string    `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// Usage contains server-reported token accounting, not client estimates.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptCacheHitTokens    int                      `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int                      `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details"`
}

// PromptTokensDetails describes prompt cache accounting.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// CompletionTokensDetails describes reasoning token accounting.
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ModelList contains the available models and HTTP metadata.
type ModelList struct {
	ResponseMetadata `json:"-"`
	Object           string  `json:"object"`
	Data             []Model `json:"data"`
}

// Model describes server-reported model capabilities.
type Model struct {
	ID               string          `json:"id"`
	Object           string          `json:"object"`
	OwnedBy          string          `json:"owned_by"`
	Name             string          `json:"name"`
	ContextWindow    int             `json:"context_window"`
	MaxOutputTokens  int             `json:"max_output_tokens"`
	InputModalities  []string        `json:"input_modalities"`
	OutputModalities []string        `json:"output_modalities"`
	Effort           *Effort         `json:"effort"`
	APICapabilities  json.RawMessage `json:"api_capabilities"`
}

// Effort describes the supported and default reasoning levels.
type Effort struct {
	SupportedLevels []string `json:"supported_levels"`
	DefaultLevel    string   `json:"default_level"`
}

// Balance contains account availability and decimal-string balances.
type Balance struct {
	ResponseMetadata `json:"-"`
	IsAvailable      bool          `json:"is_available"`
	BalanceInfos     []BalanceInfo `json:"balance_infos"`
}

// Monetary amounts remain decimal strings to avoid floating-point rounding.
type BalanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}
