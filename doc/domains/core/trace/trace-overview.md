# trace 领域总览

## Identity

| 项 | 值 |
|----|----|
| 领域名 | trace(结构化 trace 日志 + debug + hooks 可观测) |
| 业务组 | core |
| 一句话职责 | opt-in 的异步事件落盘子系统:把 vage 代理生命周期的 `schema.Event` 全量写为按项目散列 + 会话 id 分目录的 JSONL;并统辖与 trace 正交的两个可观测扩展点 —— 开发期 Debug 逐次 I/O 记录与业务侧 Hooks 扩展接口。 |

## Ownership

| 项 | 值 |
|----|----|
| 负责团队 | vv core |
| 源码目录 | `vv/traces/tracelog/`(JSONL hook)、`vv/debugs/`(Debug sink)、`vv/hooks/`(Hooks 扩展点);装配在 `vv/setup/` |

## Status

active

## Dependencies

| 上游依赖 | 关系 |
|----------|------|
| [orchestration](../orchestration/orchestration-overview.md) | **事件源**。TaskAgent 的 ReAct 循环在生命周期点 `Dispatch` `schema.Event`;trace 只旁路订阅,不反向影响编排。 |
| [configuration](../configuration/configuration-overview.md) | 装配中心从 `trace:` 块 + `VV_TRACE_*` 决定是否构造 `hook.Manager`、注入每个 TaskAgent 工厂、并接出 `InitResult.Shutdown`。 |

底层协议复用 vage 的 `hook`(AsyncHook / Manager)与 `schema`(Event)两个包。事件总线与 [session](../session/session-overview.md) 的 `events.jsonl` 落盘、[cost-tracking](../cost-tracking/cost-tracking-overview.md) 的逐次累加**共用同一条总线**(旁路订阅,互不感知)。budget 是独立领域,本领域不写预算细节(见 [../budget/](../budget/))。

## API exposure

**false**。本领域不暴露任何公开接口:trace 文件无 HTTP/CLI 查询入口(P2-14 session resume 才会读取);Debug 仅开发期 `--debug` 开启;Hooks 是进程内注册的代码扩展点。trace 文件是离线分析/复盘的旁路产物,不是用户可见资源。

## 关联 ADR

| ADR | 关系 |
|-----|------|
| 0005 事件总线旁路订阅 + 零成本默认 | 本领域的核心架构决策。trace / debug / hooks 三子系统均挂在 vage 事件总线旁路;未启用即不构造、零开销。详见 [../../../architecture/adr/adr.md](../../../architecture/adr/adr.md) 候选清单(尚未正式落盘,需用户批准)。 |

## 文档索引

| 文件 | 内容 |
|------|------|
| [spec.md](spec.md) | WHAT/WHY:核心实体、业务规则 TRACE-R*、文件轮转状态机、订阅的领域事件、交互、non-goals、anti-scenario、数据字典 |
| [design.md](design.md) | HOW:四子系统共享事件总线、Trace 异步落盘/缓冲/分目录/轮转、Trace vs Session 对比、Debug 多模式 sink + 最外层位置、Hooks 扩展点、事件优先 + 旁路订阅架构、技术取舍 |
| [models.md](models.md) | 实体模型:Trace Config、Trace Event、Trace File |

术语见 [../../../glossary.md](../../../glossary.md);trace 不脱敏的安全负空间见 [../../../non-functional/security.md](../../../non-functional/security.md)。
