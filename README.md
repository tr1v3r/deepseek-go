# deepseek-go

**English** | [简体中文](README.zh-CN.md)

A Go client for the DeepSeek API, built entirely on the standard library.
Requires **Go 1.22 or later**.

The SDK provides typed requests and responses, streaming completions, tool-call
support, structured errors, and context-based cancellation. This is an independent
project and is not an official DeepSeek SDK.

> **Status:** Pre-release. No stable version has been published, and the API may
> change.

## Supported APIs

| API | Client method |
| --- | --- |
| Chat completions | `CreateChatCompletion` |
| Streaming chat completions (SSE) | `CreateChatCompletionStream` |
| Model listing | `ListModels` |
| Account balance | `GetBalance` |

Responses, Files, Search/Fetch, and FIM endpoints are not currently wrapped.
Tool execution remains the application's responsibility.

## Installation

Install the current development version:

```sh
go get github.com/tr1v3r/deepseek-go@main
```

`@main` installs the current development revision; pin a specific revision when
you need reproducible builds.

## Quick start

Set your API key and a model ID available to your account:

```sh
export DEEPSEEK_API_KEY="your-api-key"
export DEEPSEEK_MODEL="your-model-id"
```

Save the following program as `main.go` in your Go module and run `go run .`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	deepseek "github.com/tr1v3r/deepseek-go"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	client, err := deepseek.NewClient(deepseek.Config{
		APIKey: os.Getenv("DEEPSEEK_API_KEY"),
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	response, err := client.CreateChatCompletion(ctx, deepseek.ChatCompletionRequest{
		Model: os.Getenv("DEEPSEEK_MODEL"),
		Messages: []deepseek.Message{
			deepseek.SystemMessage("Answer concisely."),
			deepseek.UserMessage("What is an API?"),
		},
		Temperature: deepseek.Ptr(0.0),
	})
	if err != nil {
		return err
	}

	for _, choice := range response.Choices {
		fmt.Println(choice.Message.Content.Text())
	}

	return nil
}
```

Use `client.ListModels(ctx)` to discover model IDs. `deepseek.Ptr` distinguishes an
omitted optional parameter from an explicit zero value. `client.GetBalance(ctx)`
returns monetary amounts as decimal strings to preserve precision.

## Streaming

The following helper uses `context`, `errors`, `fmt`, `io`, and the `deepseek`
package. Pass the same request type used for non-streaming completions:

```go
func streamChat(
	ctx context.Context,
	client *deepseek.Client,
	request deepseek.ChatCompletionRequest,
) error {
	stream, err := client.CreateChatCompletionStream(ctx, request)
	if err != nil {
		return err
	}
	defer stream.Close()

	var result deepseek.Accumulator

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		if err := result.Add(*chunk); err != nil {
			return err
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				fmt.Print(*choice.Delta.Content)
			}
		}
	}

	completion := result.Completion()
	fmt.Printf("\nCompleted choices: %d\n", len(completion.Choices))

	return nil
}
```

- `Recv` returns `io.EOF` only after the server sends `[DONE]`. A premature
  disconnect returns `io.ErrUnexpectedEOF`.
- Streams close automatically on completion or failure. Use `defer stream.Close()`
  to release resources when leaving the loop early. `Close` may interrupt a
  concurrent `Recv`; use only one reader per stream.
- `Accumulator` combines content, reasoning content, and tool arguments by
  choice/tool index. `Completion()` returns a detached, index-sorted snapshot.
  Use an accumulator from only one goroutine at a time.
- Accumulation retains the entire output in memory. Consume chunks directly if
  you do not need a complete response. A snapshot alone does not establish that
  the stream completed successfully.
- Usage retains the latest non-null value. Logprobs retain only the last non-null
  raw JSON chunk per choice; token-level logprobs are not merged.

See the runnable [streaming example](examples/stream/main.go).

## Messages and tools

| Purpose | API |
| --- | --- |
| Common message roles | `SystemMessage`, `UserMessage`, `ToolMessage` |
| Text, including an explicit empty string | `Text("hello")`, `Text("")` |
| Null content | `Content{}` |
| Content parts | `Parts(...ContentPart)` |
| Read content | `Content.Text()`, `Content.Parts()`, `Content.IsNull()` |
| Function tools | `Tool` with `FunctionDefinition` |
| Tool selection | `AutoToolChoice`, `NoToolChoice`, `RequiredToolChoice`, `NamedToolChoice` |

Function parameters use `json.RawMessage` for their JSON Schema. After receiving
a tool call, validate its arguments and execute the tool in your application.
Preserve the assistant message in the conversation, then append
`deepseek.ToolMessage(call.ID, resultJSON)` before requesting the next completion.

The content types can represent multimodal messages; actual model capabilities
and parameter restrictions are determined by the service. For beta endpoints,
explicitly set `BaseURL: "https://api.deepseek.com/beta"`.

## Configuration

Pass configuration to `deepseek.NewClient`:

| Field | Default | Behavior |
| --- | --- | --- |
| `APIKey` | Required | API authentication credential |
| `BaseURL` | `https://api.deepseek.com` | Supports a path prefix |
| `HTTPClient` | Client with a 10-minute timeout | Timeout includes reading the entire response or stream |
| `MaxRetries` | `0` | Accepts 0–10; applies only to eligible GET responses |
| `MaxResponseBytes` | 16 MiB | Maximum regular JSON response size |
| `MaxEventBytes` | 4 MiB | Maximum SSE line and event size |

A supplied `http.Client` is shallow-copied; its transport is shared. Redirects are
rejected. Use a request context for cancellation or a shorter deadline.

### Retry behavior

Retries are opt-in and apply only to GET responses with status 408, 429, 500, 502,
503, or 504. Backoff is exponential with jitter and can be canceled through the
request context. `Retry-After` supports seconds and HTTP dates; if it requests a
wait exceeding 30 seconds, the SDK returns the original error instead of retrying
early.

The SDK does not automatically retry network errors, generation POST requests,
or active streams. A custom transport may implement its own retry behavior.
`APIError.Retryable()` identifies a transient status; it does not establish that
replaying a request is safe.

## Error handling and response metadata

Use `errors.Is` and `errors.As` to inspect a returned error:

```go
var apiErr *deepseek.APIError
var validationErr *deepseek.ValidationError
var protocolErr *deepseek.ProtocolError

switch {
case errors.Is(err, context.Canceled):
	fmt.Println("Request canceled")
case errors.Is(err, context.DeadlineExceeded):
	fmt.Println("Request timed out")
case errors.As(err, &apiErr):
	fmt.Printf("API status=%d request_id=%s\n", apiErr.StatusCode, apiErr.RequestID)
case errors.As(err, &validationErr):
	fmt.Printf("Invalid field: %s\n", validationErr.Field)
case errors.As(err, &protocolErr):
	fmt.Println("Invalid or incomplete API response")
case err != nil:
	fmt.Println("Request failed")
}
```

`ValidationError` identifies invalid local request structure. `ProtocolError`
reports malformed or incomplete responses, unexpected content types, and size
limit violations; its underlying cause is available through `Unwrap`. An HTTP
200 error envelope or a missing required response field is also a protocol error.

Successful responses expose `ResponseMetadata`; streams expose a copy through
`Metadata()`. Protocol errors retain received response metadata. `APIError`
includes the status, request ID, headers, and structured API error fields. Its
response body is limited to 64 KiB, with `BodyTruncated` indicating truncation.
For errors sent inside an SSE stream, `APIError.StatusCode` is zero.

The SDK does not log credentials, prompts, or responses. Error messages, bodies,
and response headers may contain server-echoed data; redact them as appropriate
before logging in your application.

## Examples and development

- [Chat completion](examples/chat/main.go)
- [Streaming completion](examples/stream/main.go)

Run `go run ./examples/chat` or `go run ./examples/stream` from this repository
after setting the environment variables above. These examples call the live API.

For local verification:

```sh
go test -race -cover ./...
go vet ./...
go test ./internal/sse -fuzz=FuzzDecoder -fuzztime=15s
```

Tests use local mock servers and do not require an API key. CI checks Go 1.22 and
the current stable Go version on Linux, macOS, and Windows, plus formatting and
SSE fuzzing. Live DeepSeek API integration has not yet been validated.

See the [changelog](CHANGELOG.md), [migration guide](docs/migration.md), and
[contribution guide](CONTRIBUTING.md) for further details (currently in Chinese).

## Protocol references

- [DeepSeek chat completions](https://api-docs.deepseek.com/api/create-chat-completion/)
- [DeepSeek model listing](https://api-docs.deepseek.com/api/list-models/)
- [DeepSeek account balance](https://api-docs.deepseek.com/api/get-user-balance/)
- [WHATWG server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation)

The SSE decoder handles UTF-8 BOM, LF/CR/CRLF line endings, comments, and multiline
`data` fields. The SDK does not automatically reconnect streams.
