# 代码风险审查 — 2026-10-21

> 本文涉及的历史提交编号属于公开前的开发记录；公开时已将开发历史合并为单个初始提交。

范围：`d2324b5...19676d0` 的工程化改动，以及 `19676d0` 完整 SDK；规范依据为 CONTRIBUTING、README、GoDoc 与用户要求的可读性。以下是已复现的局部问题，不构成无风险或真实服务兼容性保证。

## Standards

- **P2，已修复：同一 choice/tool index 的 ID 或类型变化会覆盖旧身份。** 参数仍继续拼接，可能构成错误的工具调用。Accumulator 现在先校验整个 chunk（含 chunk 内重复索引），再修改状态；冲突原子拒绝，已有结果保持不变。
- **P2，已修复：协议失败丢失请求元数据。** 普通 JSON 格式/大小错误及 SSE 内容类型/解码错误现在在 ProtocolError 中保留 RequestID、StatusCode 和响应头副本。
- **可读性，已整理：** 顶层声明和函数阶段补空行，展开多语句单行函数及大字面量；Accumulator.Add 拆为校验、元数据、choice 累积与快照，使用角色变量名；公开类型和构造函数补语义说明。将这些约定写入 CONTRIBUTING。

## Spec

- **P2，已修复：HTTP 200 的错误 envelope / 缺失必要字段被当作成功。** 三个端点现在拒绝 error 对象和缺失/null 的 choices、data 或 is_available；保留合法空列表与 false，不硬编码模型相关 schema。回归测试同时检查响应体关闭。
- **P2，已修复：Retry-After 全数字超出整数范围时回退到短延迟。** 现在将其视为超出 30 秒重试预算，不提前重试。
- **P2，已修复：text 内容块的空字符串被 omitempty 删除。** 自定义编码保留 `"text":""`，不向 image part 增加空 text。
- **P2，已修复：末尾 JSON null logprobs 覆盖先前值。** 累积器跳过缺失或 null，只保留最后一个实际值。

## 验证与剩余边界

新增 `risk_regression_test.go` 覆盖上述路径；原有取消、Close/Recv 并发、SSE 分片与截断测试继续执行。未发现可证实的默认 Transport 生成重放、Close/Recv 数据竞争或 CRLF 跨读取分割缺陷。

- 未进行真实 DeepSeek API 联调；服务端变更与代理行为仍需独立验证。
- 累积器将完整输出保留在内存，内存预算由应用控制；logprobs 不做逐 token 合并。
- 非标准 `event: error` 顶层 message/code 仍仅保留在 Body，结构化 Message 可能是通用文本；当前未扩大兼容范围。
- 所有验证均针对当前测试样本；覆盖率不是风险消除证明。

## 全量 CR 后续修正

- 展开 contracts_test.go 的密集表格、回调以及普通/流式示例的请求构造；标准库与 SDK 导入分组。
- 退避取消测试不再依赖 50ms 请求 deadline。测试专用 context 在 429 响应体关闭后观察重试等待读取 Done，触发同步信号；随后主动取消，断言 context.Canceled 和单次请求。2 秒计时器仅用于测试防挂起。
- 该测试在 race 模式连续执行 50 次通过；临时 Go overlay 将取消分支改成继续等待 timer 时，新测试按预期失败，证明能够捕获取消回归。SDK 生产代码未因此改动。
