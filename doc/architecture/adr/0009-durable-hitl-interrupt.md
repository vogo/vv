# 0009 — Durable HITL：vage interrupt 接到 Primary 与 HTTP

- **Status**: proposed
- **Date**: 2026-10-05

## Context

vage 已有跨进程可恢复的 interrupt 状态机(`Pending → Ready → Resuming → Completed`),闸门装在 ReAct 执行前并冻结整批工具调用。vv 的 HTTP / MCP 面此前只有「全自主」和「全拒绝」两档:非交互下 `TierDangerous` 的 bash 被 permission 硬拒绝,`ask_user` 又是进程内阻塞、无法跨进程恢复。框架资产闲置,服务端形态缺少「受监督的自主」。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 继续用 `ask_user` / CLI 对话框 | 零新接线 | 无持久化、不可跨进程;HTTP 只能硬拒绝 |
| 占用 `pending_interaction` 事件 | 复用现有 HTTP 回调 | 语义是 ask_user 阻塞,不是执行前冻结;无租约 |
| **装配 vage interrupt + HTTP 三端点(选定)** | 复用已建成的状态机;批准后可原地继续 | 需补「批准后执行 handler」;不能给 ephemeral worker 接线 |

## Decision

1. vage 增加通用 `interrupt.Decision.Execute`(不注入 vv 概念)。`IsError` 优先。Resume 时 Execute 走原 handler,否则注入 Content。流式经 `EmitterFromContext` 发出已有的 `interrupt_created`。批准执行时 context 带 `interrupt.WithApprovedExecute`,permission 仅在此标记下跳过非交互 Dangerous 硬拒绝——worker 等未冻结的路径仍硬拒绝。
2. vv 默认关闭(`agents.interrupt_enabled=false`)。开启时要求 `session.enabled` 且 `bash_rules` 未关;FileStore 根为 `<session-root>/interrupts/`(与 ADR-0004 共项目根,记录按 interrupt id 而非 session id)。策略 `dispatches.NewDangerousBashPolicy` 只拦 bash + `TierDangerous`。接线对象:**Primary + 长期存活的 ProfileFull(coder)**。派生 worker **不接**(实例 ID 即用即弃,`ResumeInterrupt` 会 AgentID mismatch;nested HITL 仍是 vage TOOL-6b / AC-16)。
3. HTTP:`GET /v1/sessions/{id}/interrupts`、`POST /v1/interrupts/{id}/decisions`、`POST /v1/interrupts/{id}/resume`。决策走 vage `Decision`(含 `execute`);resume 带空 `schema` Decisions。未启用则不挂路由。删除 session 时 List+Delete 该 session 的 interrupt 记录。
4. CLI 默认路径不变。`TierBlocked` 始终硬拒绝。

## Consequences

- ✅ HTTP 面可以把危险但合法的 bash 挂起、等人批准、原地继续,而不必放开全部护栏或作废整轮。
- ✅ 关闭时零成本:不建 store、不传 option。
- ⚠️ interrupt 记录不在 `<root>/<session-id>/` 下,删除会话必须额外清理,不能只靠 `os.RemoveAll`。
- ⚠️ 派生 worker 的危险 bash 在 HTTP 下仍硬拒绝;需要 HITL 的操作应留在 Primary / coder。
- 不改 largemodel EventType;不把 bash 分类器注入 vage。

## Compliance

- 代码:store 与 policy both-or-neither;`AppendInterrupt` 仅 Full 档与 Primary;permission 用 `IsApprovedExecute` 而非进程级开关。
- 测试:策略只拦 Dangerous;非交互硬拒绝 vs 批准 resume 放行;Blocked 仍拒绝;HTTP list/decisions/delete;装配失败条件(无 session / 无 classifier)。

## References

- `vage/doc/architecture/adr/0001-interrupt-independent-state-machine.md`
- [0004](0004-shared-root-session-directory.md)
- `doc/domains/core/orchestration/` ORCH-R15、`doc/domains/core/http-api/` HTTP-R9
- 代码:`vage/interrupt/`、`vv/dispatches/interrupt_policy.go`、`vv/setup/interrupt.go`、`vv/httpapis/interrupt.go`
