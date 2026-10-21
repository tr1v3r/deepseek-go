// Package deepseek implements DeepSeek Chat Completions, streaming completions,
// model discovery, and account balance queries using Go's standard library.
//
// Create a Client with NewClient and reuse it across requests. All operations
// accept a context. Streaming callers must close streams when stopping early.
// Content is a typed union: use Text, Parts, or the null zero value. UserMessage
// and SystemMessage are conveniences for text-only conversations.
//
// APIError, ValidationError, and ProtocolError support errors.As. Context and
// transport errors retain their causes for errors.Is. The SDK never logs keys,
// prompts, or responses. HTTPClient.Transport can provide custom observability.
//
// No retries occur by default. Config.MaxRetries enables bounded retries for
// model and balance GETs only. Generation requests are not replayed or resumed.
package deepseek
