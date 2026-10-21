# deepseek-go

[English](README.md) | **简体中文**

基于 Go 标准库实现的 DeepSeek API 客户端，要求 **Go 1.22 或更高版本**。

SDK 提供类型化请求与响应、流式输出、工具调用支持、结构化错误和基于 context 的取消机制。本项目为独立开发项目，非 DeepSeek 官方 SDK。

> **项目状态：** 当前处于预发布阶段，尚无稳定版本，API 仍可能调整。

## 支持的 API

| API | 客户端方法 |
| --- | --- |
| 对话补全 | `CreateChatCompletion` |
| 流式对话补全（SSE） | `CreateChatCompletionStream` |
| 模型列表 | `ListModels` |
| 账户余额 | `GetBalance` |

当前未封装 Responses、Files、Search/Fetch 和 FIM 端点。工具执行由应用负责。

## 安装

安装当前开发版本：

```sh
go get github.com/tr1v3r/deepseek-go@main
```

`@main` 安装当前开发版本；需要可复现构建时，请固定具体提交版本。

## 快速开始

设置 API Key 和账户可用的模型 ID：

```sh
export DEEPSEEK_API_KEY="your-api-key"
export DEEPSEEK_MODEL="your-model-id"
```

将以下程序保存为 Go 模块中的 `main.go`，执行 `go run .`：

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

通过 `client.ListModels(ctx)` 可以查询模型 ID。`deepseek.Ptr` 用于区分未传入可选参数和显式传入零值。`client.GetBalance(ctx)` 将金额保留为十进制字符串，避免精度损失。

## 流式输出

以下辅助函数使用 `context`、`errors`、`fmt`、`io` 和 `deepseek` 包。请求类型与非流式对话相同：

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

- 只有收到服务端的 `[DONE]`，`Recv` 才返回 `io.EOF`；连接提前断开时返回 `io.ErrUnexpectedEOF`。
- 流在完成或失败时自动关闭。使用 `defer stream.Close()` 确保提前退出循环时释放资源。`Close` 可以中断并发进行的 `Recv`；每个流只能有一个读取者。
- `Accumulator` 按 choice/tool 索引拼接正文、推理内容和工具参数。`Completion()` 返回独立的、按索引排序的快照。同一个累积器应由单个 goroutine 使用。
- 累积器在内存中保留完整输出。不需要完整响应时，可直接消费 chunk。仅有快照并不代表流已成功结束。
- usage 保留最新非空值；logprobs 仅保留各 choice 最后一个非空原始 JSON chunk，不进行 token 级合并。

完整可运行代码见[流式示例](examples/stream/main.go)。

## 消息与工具

| 用途 | API |
| --- | --- |
| 常用消息角色 | `SystemMessage`、`UserMessage`、`ToolMessage` |
| 文本及显式空字符串 | `Text("hello")`、`Text("")` |
| Null 内容 | `Content{}` |
| 多部分内容 | `Parts(...ContentPart)` |
| 读取内容 | `Content.Text()`、`Content.Parts()`、`Content.IsNull()` |
| 函数工具 | `Tool` 与 `FunctionDefinition` |
| 工具选择 | `AutoToolChoice`、`NoToolChoice`、`RequiredToolChoice`、`NamedToolChoice` |

函数参数的 JSON Schema 使用 `json.RawMessage` 表示。收到工具调用后，由应用校验参数并执行工具。保留对话中的 assistant 消息，再追加 `deepseek.ToolMessage(call.ID, resultJSON)`，然后请求下一轮补全。

内容类型可以表示多模态消息，实际模型能力与参数限制由服务端决定。使用 beta 端点时，请显式设置 `BaseURL: "https://api.deepseek.com/beta"`。

## 客户端配置

通过 `deepseek.NewClient` 传入配置：

| 字段 | 默认值 | 行为 |
| --- | --- | --- |
| `APIKey` | 必填 | API 身份认证凭据 |
| `BaseURL` | `https://api.deepseek.com` | 支持路径前缀 |
| `HTTPClient` | 超时为 10 分钟的客户端 | 超时覆盖整个响应或流的读取过程 |
| `MaxRetries` | `0` | 可设为 0–10，仅重试符合条件的 GET 响应 |
| `MaxResponseBytes` | 16 MiB | 普通 JSON 响应大小上限 |
| `MaxEventBytes` | 4 MiB | SSE 行与事件大小上限 |

传入的 `http.Client` 会被浅复制，transport 仍然共享。SDK 拒绝重定向。通过请求 context 可以取消请求或设置更短的截止时间。

### 重试行为

重试需要显式开启，仅适用于状态码为 408、429、500、502、503 或 504 的 GET 响应。重试采用带抖动的指数退避，等待期间可通过请求 context 取消。`Retry-After` 支持秒数和 HTTP 日期；要求等待超过 30 秒时，SDK 返回原错误，不会提前重试。

SDK 不自动重试网络错误、生成类 POST 请求或已经建立的流。自定义 transport 可能具有自己的重试行为。`APIError.Retryable()` 仅表示状态码属于暂时性错误，不代表重放请求一定安全。

## 错误处理与响应元数据

通过 `errors.Is` 和 `errors.As` 判断返回的错误：

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

`ValidationError` 表示本地请求结构校验失败。`ProtocolError` 表示响应格式错误、响应不完整、内容类型不符合预期或大小超限，可通过 `Unwrap` 获取底层原因。HTTP 200 响应中的 error 对象或必要字段缺失也会作为协议错误返回。

成功响应提供 `ResponseMetadata`；流通过 `Metadata()` 返回元数据副本。协议错误保留已经收到的响应元数据。`APIError` 包含状态码、请求 ID、响应头和结构化 API 错误字段，响应体最多保留 64 KiB，截断情况通过 `BodyTruncated` 标识。SSE 流内错误的 `APIError.StatusCode` 为零。

SDK 不记录凭据、提示词或响应。错误消息、响应体及响应头可能包含服务端回显的数据，应用记录日志前应按需要脱敏。

## 示例与开发

- [对话补全](examples/chat/main.go)
- [流式补全](examples/stream/main.go)

设置上述环境变量后，在仓库目录执行 `go run ./examples/chat` 或 `go run ./examples/stream`。这些示例会调用真实 API。

本地验证命令：

```sh
go test -race -cover ./...
go vet ./...
go test ./internal/sse -fuzz=FuzzDecoder -fuzztime=15s
```

测试使用本地模拟服务，无需 API Key。CI 覆盖 Go 1.22 与当前 stable 版本，以及 Linux、macOS、Windows 三个平台，另有格式检查和 SSE fuzz 测试。目前尚未完成真实 DeepSeek API 联调。

更多信息见[变更记录](CHANGELOG.md)、[迁移指南](docs/migration.md)和[贡献指南](CONTRIBUTING.md)。

## 协议参考

- [DeepSeek 对话补全](https://api-docs.deepseek.com/api/create-chat-completion/)
- [DeepSeek 模型列表](https://api-docs.deepseek.com/api/list-models/)
- [DeepSeek 账户余额](https://api-docs.deepseek.com/api/get-user-balance/)
- [WHATWG Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation)

SSE 解码器支持 UTF-8 BOM、LF/CR/CRLF 换行、注释及多行 `data` 字段。SDK 不自动重连流。
