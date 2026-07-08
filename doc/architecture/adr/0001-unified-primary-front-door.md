# 0001 — 统一前门 Primary 替代三段管道

- **Status**: proposed
- **Date**: 2026-06-02

## Context

vv 早期采用 `intent → execute → summarize` 三段管道:先用一次 LLM 调用做意图分类,再执行,再用一次 LLM 调用汇总。每段都需独立 LLM 调用,路由逻辑硬编码在管道里,新增专家代理要改多处。上层(CLI/HTTP/MCP)还需为不同代理写不同入口。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 保留三段管道 | 阶段职责清晰 | 每请求多次 LLM 调用、路由僵硬、新增专家成本高、入口分散 |
| **统一 Primary(选定)** | 单次 ReAct 循环内自主路由、单一入口、新增专家=多挂一个工具 | 路由质量依赖模型;需递归阀门防失控(见 ADR-0002) |
| 规则引擎前置路由 | 确定性 | 规则维护成本高、无法泛化到开放任务 |

## Decision

废弃三段管道。对外只暴露一个 `agent.StreamAgent`(Dispatcher),它只把请求转发给统一的 **Primary Assistant**。Primary 是 ReAct 循环,每轮自行选择:直答 / 只读探查 / 委派 `delegate_to_<专家>` / 规划 `plan_task`。闲聊与探查由 Primary 内联承担,故 `chat`、`explorer` 独立代理被移除。路由从"前置分类"转为"模型以工具调用承担"。

## Consequences

- ✅ 上层入口统一;新增专家只需给 Primary 多挂一个 `delegate_to_*` 工具。
- ✅ 省去 intent/summarize 两次 LLM 调用。
- ⚠️ 必须配合递归深度硬阀门(ADR-0002)防止 Primary↔子代理无限递归。
- ⚠️ `ClassifyResult` / `IntentResult` / `SummaryPolicy` 成为仅测试引用的死类型,待清理。
- 迁移:`vv/dispatches/dispatch.go` 对缺失 Primary 报 "classical pipeline removed";Fallback 路径发一对静态 `summarize` phase 事件以保持 SSE 事件形状不变。

## Compliance

- 代码层:Dispatcher 不得重新引入意图分类调用;路由必须经 Primary 工具调用。
- 评审:任何"前置分类"PR 需引用本 ADR 说明理由。

## References

- `specs/domains/core/orchestration/`(spec.md / design.md)
- `specs/architecture/architecture.md` § 统一前门
- 代码:`vv/dispatches/`、`vv/agents/primary.go`
- 相关:[0002](0002-recursion-depth-hard-valve.md)
