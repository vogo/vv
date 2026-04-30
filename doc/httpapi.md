# HTTP 模式

## 形态

`mode: http` 启动一个长驻 HTTP 服务，暴露 vv 的代理能力给远程客户端（Web 前端、其他服务）。HTTP 模式是 **无终端** 的——任何需要交互的能力都通过异步回调实现。

## 路由结构

```
/                              ← 代理 SSE/REST（vage service 提供）
/v1/memory/...                 ← 持久化记忆 CRUD
/v1/interactions/{id}/respond  ← ask_user 异步回调
/v1/budget                     ← 预算用量快照
/v1/eval/run                   ← 评测执行（启用时）
/v1/sessions/...               ← 会话元数据 + 事件流（启用时）
/v1/sessions/{id}/workspace/   ← Plan Workspace 视图（启用时）
/v1/sessions/{id}/tree/        ← Session Tree（启用时）
```

子系统 → 路由的映射逻辑：**子系统未启用 → 对应路由不挂**。这维持了"零成本默认路径"的一致性，也避免了暴露半禁用的 endpoint。

## 中间件链

```
请求 → request-id 标记 → 路由分发
                          │
            分支：业务路由直接 handler
                          │
            分支：" / " 入口 → cost 丰富 → budget 拦截 → vage service handler
```

设计要点：

- **request-id 永远开**：成本可忽略，但对追踪长链问题至关重要。
- **cost 丰富在外层**：响应里的 token usage 信息加上 USD 折算，让前端不需要自带价格表。
- **budget 拦截在内层**：当请求触发预算超限错误时，先被拦截转换为 HTTP 429（带详细 JSON），不让原错误冒泡为 500。
- **budget 中间件按需挂**：完全没有 tracker 时不挂，避免空跑成本。

## ask_user 的异步模型

CLI 模式的 ask_user 是同步对话框，HTTP 模式不可能做同步——请求方等不起。所以：

1. 代理调用 ask_user 时，HTTP 服务把问题入"待回应盒"，分配一个 interaction id。
2. 服务通过 SSE 流推送 interaction 事件给前端。
3. 前端弹用户对话框，拿到回答后用 `POST /v1/interactions/{id}/respond` 写入。
4. 服务唤醒原代理调用，用户回答以工具结果形式返回给模型。
5. 整个过程有超时；超时未回应时代理收到失败，模型自行决策。

这种"反向 RPC"模式让 HTTP 模式在功能上与 CLI 等价，又不引入长连接的语义复杂度。

## 会话子系统的耦合

会话相关的三个子系统（session 元数据、Plan Workspace、Session Tree）共用一个会话目录：

```
<root>/<project>/<session-id>/
   ├── 会话元数据 + 事件流
   ├── plan workspace 文件
   └── session tree 文件
```

这种共根设计带来一个 **关键好处**：删除会话只需一次目录递归删除，所有子系统天然同步。这避免了"删完元数据还残留 workspace"这类一致性问题。

`DELETE /v1/sessions/{id}` 因此可以用最简单的实现，不需要跨子系统协调。

## SSE 与流式事件

代理路由（`/`）默认使用 SSE 推送事件流。事件类型与 CLI 模式完全一致——这是 vage 事件总线带来的统一性。前端可以基于事件类型做：

- `phase start/end` —— 显示当前阶段（`unified_primary` / 子代理执行）。
- `tool call start/end` —— 显示工具进度。
- `llm call end` —— 累加 token 统计。
- `assistant message delta` —— 增量渲染回答。

## 与 MCP 的差别

HTTP 与 MCP 都是"vv 暴露给远程"的形态，但语义不同：

| 维度 | HTTP | MCP |
|------|------|-----|
| 用户视角 | 暴露完整代理（含分发器） | 暴露 dispatchable 代理为 MCP 工具 |
| 协议 | REST + SSE | MCP（stdio 或 Streamable HTTP） |
| 上游 | Web 前端、自家应用 | 上游 LLM IDE（让外部 LLM 调用 vv 代理） |
| 状态 | 持久化会话可见 | 通常单次工具调用 |

如果上游是另一个 LLM 应用，MCP 才是合适的形态；HTTP 适合自有用户界面。
