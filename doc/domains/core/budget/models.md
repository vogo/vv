# budget 领域实体模型(models)

本文件给出 budget 领域三个实体的领域模型(用途 + 属性 + 关系 + 状态)。完整字段、env 覆盖、YAML 路径等细粒度引用 vv-prd,不在此复述:

- [vv-prd/models/core/budget/model-budget-config.md](../../../../vv-prd/models/core/budget/model-budget-config.md)
- [vv-prd/models/core/budget/model-budget-tracker.md](../../../../vv-prd/models/core/budget/model-budget-tracker.md)
- [vv-prd/dictionaries/core/dictionary-budget-scope.md](../../../../vv-prd/dictionaries/core/dictionary-budget-scope.md)

---

## Budget Config

**用途**:`vv.yaml` 中 `budget:` 块映射出的声明式上限,驱动 session / daily 两层 enforcement。每字段 opt-in——零值禁用该项;空 `budget` 块整体关闭 enforcement(零成本路径,BUDGET-R5)。

**属性**(摘要,完整见 [vv-prd model-budget-config](../../../../vv-prd/models/core/budget/model-budget-config.md)):

| 属性 | 类型 | 必填 | 语义 |
|------|------|------|------|
| session_hard_tokens | number | 否 | session 维 token 上限;0 禁用 |
| session_hard_cost_usd | number(float) | 否 | session 维 USD 上限;0 禁用;无价格表静默跳过 |
| daily_hard_tokens | number | 否 | daily 维 token 上限(UTC 滚动);0 禁用 |
| daily_hard_cost_usd | number(float) | 否 | daily 维 USD 上限(UTC 滚动);0 禁用 |
| warn_percent | number(0.0–1.0) | 否 | 软告警阈值,session 与 daily 共享;默认 0.8 |

**关系**:

| 对端 | 关系 | 说明 |
|------|------|------|
| Configuration | Extends | `budget:` 是 Configuration 的顶层块 |
| Budget Tracker | Produces | 匹配的 Session-* / Daily-* 字段构造对应 Tracker;无限制 → nil Tracker(禁用层) |
| Model Pricing(cost-tracking) | Uses | cost 维上限借价格表把 token 折算为 USD;缺价格表静默禁用 cost 维 |

**状态**:无运行期状态——纯声明式,启动期加载后不可变(Non-goal:不做运行期重载)。

---

## Budget Tracker

**用途**:单一 scope(session 或 daily)的运行期、并发安全累加器。pre-call `Check` 门控下一次 LLM 调用;post-call `Add` 计入 LLM 返回的用量并报告 warn / exceeded 转换;`Snapshot` 给只读视图。

**属性**(摘要,完整见 [vv-prd model-budget-tracker](../../../../vv-prd/models/core/budget/model-budget-tracker.md)):

| 属性 | 类型 | 语义 |
|------|------|------|
| scope | enum(Budget Scope) | `session` 或 `daily`(run 由 TaskAgent Run Budget 提供,不用本模型) |
| used_tokens | number | 当前窗口已耗 token(input + output;cache-read 已含于 input,不重复计) |
| used_cost_usd | number(float) | 当前窗口已耗 USD;无价格表时为 0 |
| hard_tokens | number | 本 scope token 上限;0 禁用该维 |
| hard_cost_usd | number(float) | 本 scope USD 上限;0 禁用该维 |
| warn_percent | number(float) | 软告警阈值份额;默认 0.8 |
| warn_fired | boolean | 一次性标志;daily 窗口滚动时重置(BUDGET-R3) |
| window_start | timestamp | 当前窗口起点;session = 构造时刻,daily = 最近 UTC 午夜 |

**关系**:

| 对端 | 关系 | 说明 |
|------|------|------|
| Budget Config | Constructed from | 匹配的 Session-* / Daily-* 字段决定是否构造 |
| Budget Scope | Categorized by | `scope` 取值自 Budget Scope 字典 |
| Token Usage | Consumes | 每次 LLM Token Usage 喂入 `Add` 推进计数 |
| Model Pricing | Consults(间接) | 中间件预算好 cost 传入;Tracker 本身 pricing-agnostic |
| Budget Enforcement 流程 | Driven by | 流程持有 `Check → LLM → Add` 顺序与事件分发 |

**状态**:见 [spec.md](spec.md) 状态机(UnderWarn → Warned → Exceeded → Rejected;daily 窗口滚动回 UnderWarn)。nil Tracker 是合法"禁用层"哨兵——所有方法 no-op。token 与 cost 两维独立,first-to-exceed 决定拒绝维度(BUDGET-R6)。daily 状态进程内,重启清零(P1-3;持久化 deferred P1-6)。

---

## Budget Scope

**用途**:标识一个限额 / 计数器所属的聚合层,供 Budget Tracker 与 enforcement 流程判定"在哪检查、更新哪个计数器"。

**值**(完整见 [vv-prd dictionary-budget-scope](../../../../vv-prd/dictionaries/core/dictionary-budget-scope.md)):

| 值 | 描述 | 窗口 |
|----|------|------|
| run | 一次 `Agent.Run` 调用 | 每 run;下次 run 重置 |
| session | 一次 CLI 会话或一个 HTTP 进程 | 进程生命周期 |
| daily | UTC 自然日 | UTC 00:00 滚动 |

**关系**:三 scope 嵌套 `run ⊂ session ⊂ daily`。run 由 orchestration 的 TaskAgent Run Budget 在代理迭代内 enforce;session / daily 由 Budget Tracker 在 LLM 调用链上 enforce。内层更紧可先触发,外层兜底。

**状态**:静态字典枚举,无状态转换。
