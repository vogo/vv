# 0008 — 预算硬阀门 + 软告警

- **Status**: proposed
- **Date**: 2026-06-02

## Context

LLM 调用产生真实成本。无上限时,失控的代理循环或异常请求可能产生大额费用。需要一种在成本产生**之前**就能阻断的机制,且默认不应给无预算需求的用户增加任何开销。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 仅事后统计成本 | 透明 | 不能阻止超支,只能事后发现 |
| **预算硬阀门 + 软告警(选定)** | 在网络调用前拒绝;80% 预警 | 需最外层中间件;daily 计数取舍 |
| 限流(rate limit) | 限频 | 限频不等于限成本 |

## Decision

预算是硬性约束,三层 scope:Run(TaskAgent 既有)、Session、Daily(UTC 按日重置)。**Budget Middleware** 是最外层 LLM 中间件,每次调用前预检配置的 tracker,超限以 `*budgets.BudgetExceededError` 拒绝(满足 `errors.Is(err, ErrBudgetExceeded)`)——**在调用到达网络前**。HTTP 模式下该错误重写为 **429**。软告警:首次越过 `warn_percent × limit`(默认 80%)发一次性 `EventBudgetWarn`;硬上限命中发 `EventBudgetExceeded`。度量 token 数与 USD 成本(基于可配置价格表,见 cost-tracking)。Daily 计数进程内、UTC 00:00 滚动、**重启清零**(有意取舍,避免跨进程协调)。**零成本路径**:无任何限制配置时不构造 tracker、不挂中间件、无额外延迟。

## Consequences

- ✅ 超支在成本产生前被阻断,而非事后发现。
- ✅ 无预算配置时零开销。
- ⚠️ daily 计数重启清零(持久化 deferred;可复用 sqlite DB 后补,见 ADR-0006)。
- 不做 per-user/tenant 预算桶(依赖认证,out of scope);不做实时通知(仅软告警事件)。

## Compliance

- 代码:预算检查必须在最外层 LLM 中间件、网络调用前执行;HTTP surface 必须把预算错误映射为 429。
- 测试(必测不变量):超限在网络调用前拒绝;`errors.Is(err, ErrBudgetExceeded)`;软告警仅一次(见 testing-strategy)。

## References

- `specs/domains/core/budget/`、`specs/domains/core/cost-tracking/`、`specs/constitution.md` § 4
- 代码:`vv/setup/`(中间件链)、预算 tracker
- 相关:[0006](0006-pure-go-sqlite-memory-backend.md)
