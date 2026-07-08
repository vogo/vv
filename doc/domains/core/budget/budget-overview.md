# budget 领域总览

- **领域名 / 组**:budget / core
- **一句话职责**:把配置的 token / USD 硬上限装配为 Run / Session / Daily 三层预算,在每次 LLM 调用到达网络前预检,超限拒绝、命中 80% 软告警,并向 CLI / HTTP 暴露三层快照。
- **Identity**:预算执行(budget enforcement)是 vv 的成本硬阀门。它与 [cost-tracking](../cost-tracking/cost-tracking-overview.md) 共用同一份 LLM 中间件统计数据,但二者职责互补:cost-tracking 纯观测,budget 是 **enforcement**(唯一能拒绝调用的成本子系统)。
- **Ownership**:平台 / 核心运行时
- **Status**:active
- **Dependencies**:上游依赖 [configuration](../configuration/configuration-overview.md)(装配中心从 `budget:` 块构造 Tracker,无限制配置时不装配中间件);消费 [cost-tracking](../cost-tracking/cost-tracking-overview.md) 的 Model Pricing 把 token 折算为 USD。Run-scope 预算由 orchestration 的 TaskAgent Run Budget 提供(与本领域互补,不在本领域 enforcement 路径上)。
- **API exposure**:true。经 [http-api](../http-api/http-api-overview.md) 暴露 `GET /v1/budget`(渲染 Session / Daily 快照);亦经 [cli](../cli/cli-overview.md) 提供 `/budget`(渲染 Run / Session / Daily 三层)。HTTP 模式下预算拒绝由 budget-error 中间件 surface 为 429。本领域不直接持有 HTTP 路由,仅提供被查询的 Tracker 句柄与拒绝错误。

## 关键不变量(摘要)

| 不变量 | 一句话 |
|--------|--------|
| 预算硬阀门 | 超过 session / daily 上限必须在 LLM 调用到达网络前拒绝(`errors.Is(err, ErrBudgetExceeded)`),HTTP→429(constitution § 4) |
| 一次性软告警 | 首次越过 `warn_percent × limit`(默认 80%)每层每窗口发一次 `EventBudgetWarn` |
| Daily UTC 滚动 | Daily 窗口在 UTC 00:00 滚动清零;进程内计数,重启清零(有意取舍) |
| 零成本默认 | 无限制配置时不构造 Tracker、不装配中间件、无额外延迟 |

完整规则见 [spec.md](spec.md)(BUDGET-R*)。

## 关联候选 ADR

- **ADR 0008 预算硬阀门 + 软告警** —— LLM 调用前预检,超限拒绝(HTTP 429),80% 软告警。本领域的核心架构决策。详见 [../../../architecture/adr/adr.md](../../../architecture/adr/adr.md) 候选清单(尚未正式落盘,需用户批准)。
- 关联 **ADR 0005 事件总线旁路订阅 + 零成本默认** —— 告警 / 超限事件经事件总线发出;无限制配置即不构造。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、三 scope、不变量(BUDGET-R*)、消耗 / 告警 / 超限状态机、领域事件、Interactions、Non-goals、Anti-scenario、数据字典 |
| [design.md](design.md) | 中间件最外层位置、硬阀门 + 告警阈值、token vs USD 两度量、daily 进程内计数取舍、价格表可覆盖、零成本路径、三层快照渲染、技术取舍 |
| [models.md](models.md) | Budget Config、Budget Tracker、Budget Scope 实体模型 |

## vv-prd 对照

- 模型:[../../../../vv-prd/models/core/budget/](../../../../vv-prd/models/core/budget/)(model-budget-config.md、model-budget-tracker.md)
- 流程:[../../../../vv-prd/procedures/core/budget/procedure-budget-enforcement.md](../../../../vv-prd/procedures/core/budget/procedure-budget-enforcement.md)
- 字典:[../../../../vv-prd/dictionaries/core/dictionary-budget-scope.md](../../../../vv-prd/dictionaries/core/dictionary-budget-scope.md)
- 源码:`vv/traces/budgets/`(tracker.go、wire.go、error.go)、`vv/httpapis/budget.go`、`vv/cli/budget.go`
- 术语:[../../../glossary.md](../../../glossary.md);预算硬阀门不变量见 [../../../constitution.md](../../../constitution.md) § 4
