# 0002 — 递归深度硬阀门 + Fallback Primary

- **Status**: proposed
- **Date**: 2026-06-02

## Context

统一前门(ADR-0001)下,Primary 可委派子代理,子代理可能再触发 Primary(经 ask_user 链或再次进入分发器),存在无限递归风险。需要一个可靠的终止机制。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| try/limit 计数后报错 | 简单 | 达限即 abort,用户拿不到回应;依赖各路径自觉计数 |
| **硬阀门 + 无工具 Fallback Primary(选定)** | 物理消除再次递归;有限步骤内必定回应 | 需维护双实例(Primary + Fallback) |
| 全局信号量 | 限并发 | 不解决单链深度问题 |

## Decision

所有委派路径经 `context.Context` 携带 **递归深度**。Dispatcher 入口统一检查上限:深度 < 上限走完整工具的 Primary;深度 ≥ 上限切换到 **Fallback Primary** —— 共享 Primary 人格与提示词,但**无任何工具**、最大迭代 1。它无论如何只能直答,物理上消除"再次委派/规划"的可能。`delegate_to_*` 与 `plan_task` 各自把深度 +1 后传给下层;DAG 共享同一递归预算(Primary 预算 +1)。

## Consequences

- ✅ 递归失控被物理阻断,而非依赖计数自觉。
- ✅ 用户在任何情况下都能在有限步骤内得到回应。
- ⚠️ 装配中心需构造两个 Primary 实例(双实例);Primary 的 `plan_task` 工具需分发器引用,采用"先创建空壳分发器再回填 Primary"的后置注入。
- Fallback 路径额外发一对静态 `summarize` phase 事件(零 LLM 调用),保持 SSE 消费者无需分支判断。

## Compliance

- 测试:必须覆盖"达深度上限切 Fallback""委派 +1 正确传递"(见 `specs/testing/testing-strategy.md`)。
- 代码:任何新增的委派/规划路径必须经 context 传递并 +1 递归深度。

## References

- `specs/domains/core/orchestration/`、`specs/architecture/architecture.md` § 递归预算
- `specs/constitution.md` § 5
- 代码:`vv/dispatches/dispatch.go`、`vv/agents/primary.go`
- 相关:[0001](0001-unified-primary-front-door.md)
