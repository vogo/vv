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

1. 批准按 call id 生效,不是整批开关。Resume 只把本批里 pending、Execute 且非 IsError 的 id 放进已批准集合;每个工具调用在进入 handler 前带上自己的 executing id。permission 仅当 `IsApprovedCall` 命中当前 executing id 时,才跳过非交互 Dangerous 硬拒绝。同批其他调用、空 id、未接线路径仍硬拒绝。`TierBlocked` 在批准检查之前返回。
2. 冻结记录带策略指纹。指纹由 vv 从 bash 规则组装结果计算(guardian 开启时含排序后的允许目录与工作目录),vage 只存不解析。resume 时指纹不一致且仍有 flagged 调用:不执行旧决策,返回新的 Pending interrupt(HTTP 200,body 里是新的 interrupt id)。无法确认指纹且不能建后继(没有 Witness、flagged 为空、评估不一致)返回 policy drift,不拿租约。version 2 记录没有指纹,仍按 call 批准后执行,不改写成 version 3。
3. vv 默认关闭(`agents.interrupt_enabled=false`)。开启时要求 `session.enabled` 且 `bash_rules` 未关;FileStore 根为 `<session-root>/interrupts/`(与 ADR-0004 共项目根,记录按 interrupt id 而非 session id)。策略只拦 bash + `TierDangerous`,并实现 Witness。接线闸门是 `CapInterrupt`:只有 ProfileFull 声明它,Primary 用自己的 tool profile 装配,不看描述符上的 ReadOnly。派生 worker 与 Fallback Primary **不接**(实例 ID 即用即弃;nested HITL 仍不受支持)。
4. HTTP:`GET /v1/sessions/{id}/interrupts`、`POST /v1/interrupts/{id}/decisions`、`POST /v1/interrupts/{id}/resume`。决策走 interrupt `Decision`(含 `execute`);resume 带空 schema Decisions。指纹无法建后继时 resume 返回 409 `policy_drift`;建了后继则 200,且 body 的 interrupt id 是新 id。`POST .../decisions` 不跟随 Supersedes。未启用则不挂路由。删除 session 时 List+Delete 该 session 的 interrupt 记录。
5. CLI 默认路径不变。`TierBlocked` 始终硬拒绝。启动时审计 interrupt 目录:不可读版本只记错误日志,不删文件,不因此启动失败。`.lock` 与临时文件不计入。

## Consequences

- ✅ HTTP 面可以把危险但合法的 bash 挂起、等人批准、原地继续,而不必放开全部护栏或作废整轮。
- ✅ 关闭时零成本:不建 store、不传 option。
- ⚠️ interrupt 记录不在 `<root>/<session-id>/` 下,删除会话必须额外清理,不能只靠 `os.RemoveAll`。
- ⚠️ 派生 worker 的危险 bash 在 HTTP 下仍硬拒绝;需要 HITL 的操作应留在 Primary / coder。
- 不改 largemodel EventType;不把 bash 分类器注入 vage。

## Compliance

- 代码:store 与 policy both-or-neither;只有声明 `CapInterrupt` 的长期宿主(ProfileFull 与 Primary)装配 interrupt;worker 不读该能力。permission 用已批准 call id 与当前 executing id,不是整批开关。指纹在 vv 计算,vage 只存储。
- 测试:策略只拦 Dangerous;非交互硬拒绝 vs 批准 resume 放行;Blocked 仍拒绝;HTTP list/decisions/delete;装配失败条件(无 session / 无 classifier)。

## References

- `vage/doc/architecture/adr/0001-interrupt-independent-state-machine.md`
- [0004](0004-shared-root-session-directory.md)
- `doc/domains/core/orchestration/` ORCH-R15、`doc/domains/core/http-api/` HTTP-R9
- 代码:`vage/interrupt/`、`vv/dispatches/interrupt_policy.go`、`vv/setup/interrupt.go`、`vv/httpapis/interrupt.go`
