# http-api 领域设计(design)

> 实现细节(具体处理函数、序列化)以 `vv/httpapis/` 源码为准,此处只记录抬不进代码的取舍与跨文件结构。

## 形态

`mode: http` 启动一个长驻 HTTP 服务,把 vv 的代理能力暴露给远程客户端(Web 前端、其他服务)。HTTP 模式是**无终端**的——任何需要交互的能力都通过异步回调实现(见 §`ask_user` 的异步模型)。服务由与 CLI 同一个 `vv` 二进制以 `--mode http`(或 `VV_MODE=http`)启动;装配入口为 `vv/httpapis/http.go` 的 `Serve`。

## 路由结构

```
/                              ← 代理 SSE/REST(vage service 提供;Dispatcher + 各 agent 注册于此)
/v1/agents, /v1/agents/{id}    ← 列举 / 详情(由 vage service handler 承载)
/v1/agents/{id}/run            ← sync 同步执行
/v1/agents/{id}/stream         ← streaming SSE 执行
/v1/agents/{id}/async          ← async 异步任务
/v1/tasks/{id}, /{id}/cancel   ← async 任务查询 / 取消
/v1/health, /v1/tools          ← 健康检查 / 工具列表
/v1/memory/...                 ← 持久化记忆 CRUD(恒挂)
/v1/interactions/{id}/respond  ← ask_user 异步回调(interactionStore 存在才挂)
/v1/budget                     ← 预算用量快照(至少一个 Tracker 激活才挂)
/v1/eval/run                   ← 评测执行(eval.enabled 才挂)
/v1/sessions/...               ← 会话元数据 + 事件流 + resume/metrics(sessionStore 存在才挂)
/v1/sessions/{id}/workspace/   ← Plan Workspace 视图(planWorkspace 存在才挂)
/v1/sessions/{id}/tree         ← Session Tree(treeStore 存在才挂)
/v1/vector/...                 ← 向量检索(vectorStore + embedder 都在才挂;单缺返回 503)
```

### 子系统 → 路由的映射策略

**子系统未启用 → 对应路由不挂**。这维持了"零成本默认路径"的一致性,也避免暴露半禁用的 endpoint(对应 spec HTTP-R6)。

技术取舍:大多数可选路由用"nil store → 不 `HandleFunc`"实现;少数(resume / metrics / build-reports / vector)选择**无条件挂载但返回结构化 503**,因为它们要区分"子系统整体关闭"(404,路由不存在)与"子系统在但可选子存储缺失"(503,语义明确)。这是一个刻意的 404-vs-503 语义取舍。

## 中间件链

```
请求 → request-id 标记 → ServeMux 路由分发
                          │
            分支:业务路由(/v1/memory、/v1/sessions…)→ 直接 handler
                          │
            分支:" / " 入口 → cost 富化(外)→ budget 拦截(内)→ vage service handler
```

设计要点(对应 spec HTTP-R2/R3/R7):

- **request-id 永远开**:成本可忽略,但对追踪长链问题至关重要;故不做开关,恒挂(`requestIDMiddleware`)。
- **cost 富化在外层**:响应里的 token usage 加上 USD 折算,让前端不需要自带价格表。放外层,保证即使非预算响应也照常富化。
- **budget 拦截在内层**:请求触发预算超限错误时,先被拦截转换为 HTTP 429(带详细 JSON 信封),不让原错误冒泡为 500。
- **budget 中间件按需挂**:完全没有 Tracker 时不挂——否则那层 body 缓冲会在每个请求上空跑买单。

### 预算 429 重写

vage service 层的状态码无法被本领域直接改写,但 `vv/traces/budgets` 的错误会经代理运行路径写进**响应体**。故 `budgetErrorMiddleware` 用 `responseRecorder` 缓冲响应体,探测 JSON 体内是否含预算超限签名("budget exceeded" 文本 / `type: budget_exceeded`),命中则把状态码改写为 429。用错误文本而非紧耦合的 JSON schema 来识别,既能抓顶层 `error` 字段也能抓嵌套信封。这是对 service 层"改不了状态码"约束的窄修补。

## sync / stream / async 三路径

三条路径调用同一 Dispatcher,差异在交付通道与成本富化的落点。

```mermaid
flowchart TD
    subgraph sync["sync: POST /agents/{id}/run"]
        S1[阻塞执行 Dispatcher] --> S2[RunResponse 上加 estimated_cost_usd] --> S3[200 JSON]
    end
    subgraph stream["streaming: POST /agents/{id}/stream"]
        T1[SSE headers] --> T2[转发事件流]
        T2 --> T3[拦截 llm_call_end 累计 usage]
        T2 --> T4[心跳 15s 保活]
        T2 --> T5[agent_end 后补发 usage 事件] --> T6[关流]
    end
    subgraph async["async: POST /agents/{id}/async"]
        A1[建 Task=Pending → 202 + task_id] --> A2[后台 goroutine: Task→Running]
        A2 --> A3{成功?}
        A3 -->|是| A4[Task→Completed 存 result+usage]
        A3 -->|否| A5[Task→Failed 存 error]
        A6[GET /tasks/{id}] -.查询.-> A4
        A7[POST /tasks/{id}/cancel] -.->|Pending/Running| A8[Task→Cancelled]
    end
```

成本富化落点(对应 HTTP-R3):sync 同步加字段;streaming 拦截 `llm_call_end` 边转发边累计,流末补发 `usage` 事件;async 在任务完成时把聚合 usage 存进 task。三者复用同一价格表查询(`costtraces.LookupPricing` + 配置自定义价格),区分 cache-read token 避免重复计费。

## `ask_user` 的异步模型

CLI 模式的 `ask_user` 是同步对话框,HTTP 模式不可能做同步——请求方等不起。故采用**反向 RPC**(对应 HTTP-R4/R5):

1. 代理在 stream/async 下调用 `ask_user` 时,服务把问题入"待回应盒"(interaction store),分配 interaction id。
2. 服务通过 SSE 推送 `pending_interaction` 事件(含 id、问题、超时)给前端。
3. 前端弹用户对话框,拿到回答后用 `POST /v1/interactions/{id}/respond` 写入。
4. 服务唤醒原代理调用,用户回答以工具结果形式返回给模型。
5. 全程有超时;超时未回应时代理收到 fallback 消息,模型自行决策。每个 id 只接受一次回应(重复→409);过期记录(2× 超时)由后台 goroutine 清理。

**sync 例外**:同步请求无法支持执行中交互,`ask_user` 走非交互回退(同 CLI `-p`),立即返回不发事件。

这种"反向 RPC"让 HTTP 模式在功能上与 CLI 等价,又不引入长连接的语义复杂度。源码:`askuser.go` + `interactions.go`。

## 端点分组

| 分组 | 端点 | 归属领域 | 挂载条件 |
|------|------|----------|----------|
| Agents | run / stream / async + list / get | orchestration(经 vage service) | 恒挂 |
| Server | health / tools / tasks / cancel | http-api 自身 + async 任务存储 | 恒挂 |
| Interactions | interactions/{id}/respond | http-api(反向 RPC) | interactionStore 非 nil |
| Memory | memory CRUD | memory | 恒挂(user-path,仅共享 namespace) |
| Budget | GET /budget | budget | 至少一个 Tracker 激活 |
| Eval | POST /eval/run | eval | `eval.enabled=true`(opt-in) |
| Sessions | sessions 元数据/事件/children/patch/delete/resume/metrics/build-reports | session | sessionStore 非 nil(resume/metrics 无条件挂、缺子存储返 503) |
| Workspace | workspace plan/notes/scratch/artifacts | session(Plan Workspace) | planWorkspace 非 nil |
| Tree | sessions/{id}/tree* | session(Session Tree) | treeStore 非 nil |
| Vector | vector add/search | session(向量) | store+embedder 都在,单缺返 503 |

## 会话子系统的共根设计

会话相关的三个子系统(session 元数据、Plan Workspace、Session Tree)共用一个会话目录:

```
<root>/<project>/<session-id>/
   ├── 会话元数据 + 事件流
   ├── plan workspace 文件
   └── session tree 文件
```

关键好处:删除会话只需一次目录递归删除,所有子系统天然同步,避免"删完元数据还残留 workspace"这类一致性问题。故 `DELETE /v1/sessions/{id}` 可用最简实现,无需跨子系统协调(实现上 delete handler 在 workspace 也在场时一并清除)。

## SSE 与流式事件

代理路由(`/`)默认用 SSE 推送事件流,事件类型与 CLI 模式完全一致——这是 vage 事件总线带来的统一性。前端可基于事件类型做 phase 显示、工具进度、token 累加、增量渲染。事件类型清单见 spec.md「Domain events」。

## 与 MCP 的差别

HTTP 与 MCP 都是"vv 暴露给远程"的形态,但语义不同:

| 维度 | HTTP | MCP |
|------|------|-----|
| 用户视角 | 暴露完整代理(含分发器) | 暴露 dispatchable 代理为 MCP 工具 |
| 协议 | REST + SSE | MCP(stdio 或 Streamable HTTP) |
| 上游 | Web 前端、自家应用 | 上游 LLM IDE(让外部 LLM 调用 vv 代理) |
| 状态 | 持久化会话可见 | 通常单次工具调用 |

上游是另一个 LLM 应用时 MCP 才合适;HTTP 适合自有用户界面。

## Non-functional considerations

- **请求体上限 4MB**(SYNC-04);超出拒绝。
- **SSE 心跳 15s**(STREAM-01)防连接超时;客户端断开即停止处理(STREAM-03)。
- **async 任务存储上限默认 1000**(ASYNC-01),达上限返 429。
- **无认证 / 无多租户**(见 spec Non-goals);安全边界假定在反向代理/受信网络。
- **debug 不改契约**(HTTP-R8):debug 记录走 slog 旁路,响应体逐字节不变。

## Dependencies

| 依赖 | 类型 | 降级行为 |
|------|------|----------|
| orchestration(Dispatcher) | 必需 | 无法启动则服务不起 |
| cost-tracking(价格表) | 软 | 价格不可用 → `estimated_cost_usd=null`,不影响主响应 |
| budget(Tracker) | 可选 | 无 Tracker → budget 中间件与 `/v1/budget` 不挂(零成本) |
| session / eval / vector 等子系统 | 可选 | 未启用 → 对应路由不挂或返 503 |
| vage service | 必需 | 提供 `/` 入口、agent 注册、SSE 事件总线 |
