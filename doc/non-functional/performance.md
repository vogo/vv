# 非功能需求:性能

本文件量化 vv 的性能目标与资源约束。具体调参见各领域 `design.md` 与 `vv-prd/`。

## 零成本默认路径(核心性能原则)

未启用的可选子系统 **不构造、不挂事件、不占内存、不增延迟**。覆盖:trace、session、session_tree、budget、debug、web_search、MCP credfilter(未配置即不挂)。

| 验证点 | 约束 |
|--------|------|
| `trace.enabled` nil/false | 不构造 `hook.Manager`,不起 goroutine,不建目录,不开文件句柄 |
| 无预算配置 | 无 tracker、无中间件、无额外延迟 |
| web_search 空配置 | 工具不注册,不出现在任何代理 ToolDef |
| 未知 provider | 记 `slog.Warn` 并视为未配置,工具不出现 |

## 并发与吞吐

| 项 | 约束 |
|------|------|
| 并行工具调用 | LLM 返回多 `tool_call` 时并发执行,有界信号量默认 cap **4**,经 `agents.max_parallel_tool_calls` / `VV_AGENTS_MAX_PARALLEL_TOOL_CALLS` 可调 |
| 事件顺序确定性 | 所有 `ToolCallStart` 按 `ToolCalls` slice 序发出 → 并行执行 → `ToolCallEnd`/guard/`ToolResult` 按同序发出 |
| 串行快路径 | 单调用或 `max_parallel_tool_calls: 1` 走与并行化前一致的串行路径 |
| 错误隔离 | 一个工具失败不取消兄弟工具 |
| DAG 步并行 | 无依赖 step 并行,并发度可配 |

## 上下文窗口管理

| 机制 | 约束 |
|------|------|
| 主动 auto-compact | token 数逼近模型上限时主动压缩 |
| 反应式 emergency compact | 上下文溢出错误时紧急压缩恢复 |
| 工具输出截断 | `tool_output_max_tokens > 0` 时截断单个工具结果,防止单结果吃掉过多上下文 |
| 受保护轮次 | `context_protected_turns` 保护最近若干轮不被压缩 |
| 手动压缩 | CLI `/compact` |

相关配置项:`model_max_context_tokens`、`context_compression_threshold`、`tool_output_max_tokens`、`context_protected_turns`。

## Prompt 缓存

| 项 | 约束 |
|------|------|
| 默认断点 | 标记系统提示 + 每请求最后一个工具(Anthropic 4 个断点中的 2 个) |
| 工具排序稳定 | `tool.Registry.List()` 名称稳定排序,缓存键跨轮不漂移 |
| OpenAI 兼容后端 | 永不见 `CacheBreakpoint` 字段(`json:"-"`) |
| 可观测 | `Usage.CacheReadTokens` 闭合观测环 |
| 开关 | `cfg.Agents.PromptCaching: false` 或 `VV_AGENTS_PROMPT_CACHING=false` opt out |

## 异步与延迟约束

| 项 | 约束 |
|------|------|
| Trace 落盘 | 异步,主请求路径不等待磁盘 I/O;突发流量短期缓冲到内存;满通道非阻塞丢弃 + `slog.Warn` |
| Session 事件写入 | 异步 hook,在线请求延迟与子系统启用与否无关 |
| web_search 超时 | 默认每次调用 10s,经 `VV_WEB_SEARCH_TIMEOUT_SECONDS` 可配 |
| 优雅关闭 | 独立 3s 超时上下文执行 Shutdown |

## 资源上限

| 项 | 约束 |
|------|------|
| todo 列表 | 单列表上限 100 条 |
| Plan Workspace | `plan.md` 与每条 note 有大小上限,超限返回明确错误由模型分拆 |
| Trace 文件轮转 | `max_file_bytes > 0` 触发 `<sid>.N.jsonl` 大小轮转;`=0` 单文件不轮转 |
| web_search 结果 | 默认 5,硬上限 20(超限 clamp 而非拒绝);query 上限 1024 runes;topic 上限 64 runes |
