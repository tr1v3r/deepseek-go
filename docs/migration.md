# 从初始原型迁移

> 本文涉及的历史提交编号属于公开前的开发记录；公开时已将开发历史合并为单个初始提交。

本轮发生在首个稳定版本发布前，不承诺与初始提交 d2324b5 源码兼容。

| 初始写法 | 当前写法 |
| --- | --- |
| `Message{Role: "user", Content: "hi"}` | `UserMessage("hi")` 或 `Message{Role: RoleUser, Content: Text("hi")}` |
| `Content: []ContentPart{...}` | `Content: Parts(...)` |
| `Content: nil` | `Content: Content{}` 或不设置 |
| `message.Content.(string)` | `message.Content.Text()` |
| `Stop: "END"` | `Stop: []string{"END"}` |
| `ToolChoice: "auto"` | `ToolChoice: AutoToolChoice()` |
| 手动拼接工具参数 | `Accumulator.Add` + `Completion` |

客户端现在会在发请求前拒绝缺失 model/messages、非法 role、tool 消息缺少 tool_call_id、非正 max_tokens。JSON 响应必须是单个对象且不超过配置大小。
默认 HTTPClient 仍为 10 分钟总超时，但重定向现在被拒绝。注入的 HTTPClient 会被浅复制，之后修改它的字段不再改变 SDK 客户端；共享 Transport 自身需支持并发。

错误通过 errors.As 判断；断流、取消的 errors.Is 语义保留。没有增加默认重试。模型名和模型特有参数范围不在客户端硬编码。
