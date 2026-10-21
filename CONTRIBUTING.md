# 开发约定

- Go 1.22 是最低支持版本，仅使用标准库；新增依赖需说明必要性。
- 公开类型与行为变化同时更新 GoDoc、示例、合约测试和 CHANGELOG；pre-release 破坏性改动需写迁移说明。
- 请求/响应类型在 types.go，联合类型在 content.go；HTTP 与重试独立，SSE framing 位于 internal/sse，业务 chunk 解析位于 stream.go。
- 测试使用 httptest 或可注入 Transport，不读取真实 API key；真实联调必须独立执行，不作为默认 CI 前置。
- 提交前运行 `gofmt -w .`、`go vet ./...`、`go test -race -cover ./...`、`go test ./internal/sse -fuzz=FuzzDecoder -fuzztime=15s`。
- 回归测试应覆盖失败与资源释放，不仅是 happy path；新公开示例必须能编译。
- 使用社区 Git 作者信息。正式发版由维护者决定；当前未选择开源许可证。

## 旧 Go 与新版 macOS

Go 1.22 的内部链接器需要显式 `-B=gobuildid` 才生成 Mach-O UUID；新版 macOS 可能以 `missing LC_UUID load command` 拒绝执行。CI 仅对 macOS / Go 1.22 设置 `GOFLAGS=-ldflags=-B=gobuildid`，保留该版本的完整测试。参见 [Go 上游问题](https://go.dev/issue/68678)。稳定版 Go 不设置此兼容选项。

## 可读性

- 顶层函数、类型声明之间留一行；函数内按校验、准备、I/O、解码、返回等逻辑阶段分组。
- 多语句函数不得压成分号串；较大的结构体字面量按字段换行。
- 短构造函数可以保留单个 return，但各函数仍分开，公开函数说明空值、所有权等重要语义。
- 长流程中优先使用 choice、tool、metadata 等角色名称；只有小范围局部使用单字母变量。
- gofmt 只负责语法排版，不能代替人工判断空行和逻辑分段；review 时单独检查。
