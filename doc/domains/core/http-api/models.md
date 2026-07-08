# http-api 领域模型(models)

本领域只持久化一个实体(Async Task);其余两类是请求生命周期内的信封/事件结构。完整字段以 vv-prd 为准,此处给业务语义类型与关系。跨领域术语见 [../../../glossary.md](../../../glossary.md)。

---

## 1. Async Task

**用途**:async 模式(`POST /v1/agents/{id}/async`)下后台代理执行的句柄。跟踪状态、完成时存结果与 usage、支持取消。完整定义见 [../../../../vv-prd/models/core/server/model-async-task.md](../../../../vv-prd/models/core/server/model-async-task.md)。

| 属性 | 业务语义类型 | 必填 | 说明 |
|------|-------------|------|------|
| id | text | 是 | 创建时生成的唯一任务标识 |
| agent_id | text | 是 | 执行该任务的 agent id |
| status | enum(Task Status) | 是 | pending/running/completed/failed/cancelled,见字典 |
| result | structured | 否 | 完成时填充的代理响应 |
| error | text | 否 | 失败时填充的错误消息 |
| created_at | datetime | 是 | 任务创建时间 |
| usage | structured(Token Usage + cost) | 否 | 完成时填充:input/output/cache_read/total tokens、`estimated_cost_usd`、call_count(见 HTTP-R3) |

**关系**:每个 Task 关联一个 Agent(由该 agent 创建)。

**状态**:状态机见 [spec.md](spec.md)「States & transitions」;值与排序见 [../../../../vv-prd/dictionaries/core/dictionary-task-status.md](../../../../vv-prd/dictionaries/core/dictionary-task-status.md)。pending/running 为活动态(可取消),completed/failed/cancelled 为终态。

---

## 2. HTTP Request / Response 信封

**用途**:三种交互模式共享的请求/响应包装。请求与响应的**全字段**由 vv-prd applications/api/pages 承载(`003-run-agent` / `004-stream-agent` / `005-async-agent` 及 memory/sessions/eval 各 page);此处只列共享骨架。

### Request 信封(RunRequest)

| 属性 | 业务语义类型 | 必填 | 说明 |
|------|-------------|------|------|
| messages | structured[] | 是 | 对话消息序列 |
| session_id | text | 否 | 关联的会话 id |
| options | structured | 否 | 执行选项;含 `debug` 标志(HTTP-R8) |

- Content-Type:`application/json`;请求体上限 4MB(SYNC-04)。

### Response 信封

| 场景 | 形态 | 关键字段 |
|------|------|----------|
| sync 成功(RunResponse) | JSON, 200 | messages、usage(含 `estimated_cost_usd`)、duration |
| async 受理 | JSON, 202 | task_id |
| async 查询 | JSON, 200 | Async Task 视图(含 result/usage 或 error) |
| 错误 | JSON | `code` + `message`;预算超限重写为 429(HTTP-R2) |

**关系**:Request 经三路径之一驱动 orchestration 的 Dispatcher;sync/async 的成功 Response 由成本富化中间件加 `estimated_cost_usd`。

**状态**:无独立状态机(请求生命周期瞬态);async 受理后的后续状态归 Async Task。

---

## 3. SSE 事件

**用途**:streaming 模式(`POST /v1/agents/{id}/stream`)下经 `text/event-stream` 推送的事件流;类型与 CLI 模式一致(vage 事件总线统一)。逐事件字段见 [../../../../vv-prd/procedures/core/agent-execution/procedure-streaming-request.md](../../../../vv-prd/procedures/core/agent-execution/procedure-streaming-request.md)。

| 类别 | 事件类型 | 关键载荷 |
|------|----------|----------|
| Agent 生命周期 | agent_start / agent_end / iteration_start | — |
| 内容 | text_delta | 增量文本 |
| 工具执行 | tool_call_start / tool_call_end / tool_result | 工具名、参数、结果 |
| 编排 | phase_start / phase_end / sub_agent_start / sub_agent_end | phase 名;`sub_agent_end` 含 per-sub-agent usage(含 cache_read_tokens) |
| 用户交互 | pending_interaction | interaction id、问题文本、超时秒数(见下方 Pending Interaction) |
| 资源 | token_budget_exhausted | — |
| LLM 可观测 | llm_call_start / llm_call_end / llm_call_error | usage(被成本富化中间件拦截累计) |
| 成本汇总 | usage | input/output/cache_read/total tokens、`estimated_cost_usd`(或 null)、call_count |
| 错误 | error | 错误消息(随后关流) |

**关系**:`usage` 事件由成本富化中间件在 `agent_end` 之后补发;`pending_interaction` 触发反向 RPC 回调。

**状态**:事件流本身无状态;心跳 15s 保活(STREAM-01)。

### 附:Pending Interaction(待回应记录)

**用途**:`ask_user` 在 stream/async 下的服务端待回应记录,支撑反向 RPC(HTTP-R4)。完整流程见 [../../../../vv-prd/procedures/core/cli/procedure-http-user-question.md](../../../../vv-prd/procedures/core/cli/procedure-http-user-question.md)。

| 属性 | 业务语义类型 | 说明 |
|------|-------------|------|
| interaction_id | text(UUID) | 每次 `ask_user` 生成的唯一 id |
| question | text | 问题文本 |
| timeout | number(秒) | `ask_user_timeout` |
| status | enum | pending → responded(或超时 fallback) |

**关系**:由一次 stream/async 请求中的代理 `ask_user` 调用创建;客户端经 `POST /v1/interactions/{id}/respond` 写入回应唤醒代理。

**状态**:pending → responded;每个 id 只接受一次回应(重复→409);过期(2× 超时)由后台清理。
