# cost-tracking 领域总览

## Identity

| 项 | 值 |
|----|----|
| 领域名 | cost-tracking(token 与成本追踪) |
| 业务组 | core |
| 一句话职责 | 从 LLM 中间件统计数据累加 token usage，按可配置价格表估算 USD 成本，供 CLI 状态栏、HTTP 响应与 budget 领域消费。 |

## Ownership

| 项 | 值 |
|----|----|
| 负责团队 | vv core |
| 源码目录 | `vv/traces/costtraces/`、`vv/httpapis/cost.go`、`vv/configs/`(价格表加载) |

## Status

active

## Dependencies

本领域**无领域级依赖**。token 与成本数据全部来自 LLM 中间件链的逐次回调(post-record 闭包),不主动读取其他领域状态。

价格表条目由 configuration 领域加载并注入(YAML `model_pricing` + `VV_MODEL_PRICING` 环境变量),但运行时成本估算只接收已解析的价格表，不依赖配置领域的运行时行为。

被以下领域/接口消费(下游，非依赖):

| 消费方 | 用途 |
|--------|------|
| cli | 状态栏实时显示 model name / 累计 cost / 累计 tokens |
| http-api | sync 响应注入 `estimated_cost_usd`；streaming 末尾发 `usage` SSE 事件 |
| budget | 与 Session Cost Tracker 共享同一份逐次 Token Usage(同源、互不读取) |

## API exposure

true —— 不提供独立 endpoint，而是经 http-api 的成本富化中间件把 token usage 与 estimated cost **嵌入**已有的 run/stream 响应。详见 [design.md](design.md) 的"CLI 状态栏 vs HTTP 富化"。

## 关联 ADR

| ADR | 关系 |
|-----|------|
| 0005 事件总线旁路订阅 + 零成本默认 | 成本累加作为 LLM 中间件 post-record 旁路，不污染主路径 |
| 0008 预算硬阀门 + 软告警 | budget 领域与本领域同源；本领域只追踪估算，不做硬上限(见 [../budget/](../budget/)) |

## 文档索引

| 文件 | 内容 |
|------|------|
| [spec.md](spec.md) | WHAT/WHY：核心实体、业务规则 COST-R*、领域事件、交互、non-goals、anti-scenario |
| [design.md](design.md) | HOW：三种成本视图、同源中间件、价格查找算法、CLI/HTTP 富化、技术取舍 |
| [models.md](models.md) | 实体模型：Token Usage、Session Cost Tracker、Model Pricing |

完整字段清单回链 `vv-prd/models/core/costtracker/`。术语见 [../../../glossary.md](../../../glossary.md)。
