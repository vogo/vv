# http-api 领域总览

- **领域名 / 组**:http-api / core
- **一句话职责**:`mode: http` 下把 Dispatcher 暴露为长驻 REST + SSE 服务。**无终端**——任何交互能力(`ask_user`)都通过异步回调实现。提供三种交互模式:sync(同步阻塞)/ streaming(SSE 流式)/ async(异步任务管理),并经成本富化中间件附加 USD 折算、把预算超限错误重写为 HTTP 429。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:
  - 直接:`orchestration`(对外只暴露其单一 `agent.StreamAgent` Dispatcher;sync/stream/async 三路径都是对它的不同调用形态)。
  - 直接:`cost-tracking`(成本富化中间件复用其价格表查询与 Token Usage→USD 折算)。
  - 直接:`budget`(预算超限错误识别与 429 重写;`GET /v1/budget` 暴露其 Tracker 快照)。
  - 转发暴露(passthrough):各端点分组只做 HTTP 适配,业务逻辑归各自领域 —— `memory`(`/v1/memory/*`)、`session`(`/v1/sessions/*`、workspace、tree)、`eval`(`/v1/eval/run`)。
- **API exposure**:true(本领域是 vv 的对外 HTTP 边界;端点契约见下方 vv-prd 对照)。

## 关联候选 ADR

以下为架构级取舍,目前固化在 [../../../architecture/architecture.md](../../../architecture/architecture.md) 与各领域 spec;待评审后落为正式 ADR(编号规则见 [../../../architecture/adr/adr.md](../../../architecture/adr/adr.md)):

- **ADR-0008 预算阀门(候选)**:预算超限不在业务层抛 HTTP 状态码,而由 HTTP 边界的 budget 中间件统一把"budget exceeded"错误体重写为 **429 Too Many Requests**(带稳定 JSON 信封),避免原始错误冒泡成 500;预算超限**绝不**返回 200 放行(见 spec Anti-scenario)。中间件仅在至少一个 Tracker 激活时挂载(零成本默认路径)。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体(Async Task、HTTP 请求/响应信封)、不变量(HTTP-R*)、Async Task 状态机、SSE 域事件、各端点分组暴露关系、Non-goals、Anti-scenario、数据字典 |
| [design.md](design.md) | REST/SSE 入口与中间件链、子系统→路由的"未启用不挂"策略、sync/stream/async 三路径、成本富化中间件、预算 429 重写、`ask_user` 异步回调(反向 RPC)、端点分组与技术取舍 |
| [models.md](models.md) | Async Task、HTTP Request/Response 信封、SSE 事件三类模型的属性表、关系与状态 |

## vv-prd 对照

- 模型:[../../../../vv-prd/models/core/server/model-async-task.md](../../../../vv-prd/models/core/server/model-async-task.md)
- 应用:[../../../../vv-prd/applications/api/application-api.md](../../../../vv-prd/applications/api/application-api.md)、端点总览 [../../../../vv-prd/applications/api/pages/pages-api.md](../../../../vv-prd/applications/api/pages/pages-api.md)(各端点请求/响应全字段见其下 `pages/core/*`)
- 流程:[../../../../vv-prd/procedures/core/agent-execution/](../../../../vv-prd/procedures/core/agent-execution/)(synchronous / streaming / async)、[../../../../vv-prd/procedures/core/cli/procedure-http-user-question.md](../../../../vv-prd/procedures/core/cli/procedure-http-user-question.md)(`ask_user` 异步回调)
- 字典:[../../../../vv-prd/dictionaries/core/dictionary-task-status.md](../../../../vv-prd/dictionaries/core/dictionary-task-status.md)
- 源码:`vv/httpapis/`(`http.go` 路由装配与中间件链、`cost.go` 成本富化、`budget.go` 429 重写、`askuser.go`+`interactions.go` 异步回调、`eval.go`、`sessions*.go`、`workspace*.go`、`tree.go`)
