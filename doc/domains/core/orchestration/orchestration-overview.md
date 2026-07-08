# orchestration 领域总览

- **领域名 / 组**:orchestration / core
- **一句话职责**:vv 最核心的领域。统一前门 —— 每次请求经 Dispatcher 转交给 **Primary Assistant**,由其 ReAct 循环自行决定:直答 / 只读探查 / 委派专家 / DAG 规划;递归超限切换无工具 **Fallback Primary**。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:
  - 上游:`agents`(被委派的专家代理 + ToolProfile 能力分级)、`session`(Plan Workspace / Session Tree 镜像)。
  - 间接:`configuration`(装配 + routing 小模型客户端)、`tools`(Primary 的只读探查工具)。
  - 注:DAG 执行模型本身属 **vage**(`orchestrate` 包),本领域只提供 step 列表与节点输入映射器。
- **API exposure**:false(内部领域,无自有对外端点;经 `cli` / `http-api` / `mcp` 触发,对外表现为单一 `agent.StreamAgent`)

## 关联候选 ADR

以下为架构级取舍,目前固化在 [../../../architecture/architecture.md](../../../architecture/architecture.md) §「五条设计原则」与 [../../../constitution.md](../../../constitution.md);待评审后落为正式 ADR(编号见 `../../../architecture/adr/adr.md`):

- **ADR-0001 统一前门**:对外只暴露一个 Dispatcher,策略/路由下放到 Primary 的工具调用,替代旧 `intent → execute → summarize` 三段管道。
- **ADR-0002 递归阀门**:递归深度经 `context` 传递,Dispatcher 入口统一检查上限;超限强制切无工具 Fallback Primary,物理上(而非计数式)消除再次递归的可能。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、不变量(ORCH-R*)、Plan/Step 状态机、phase 事件、Non-goals、Anti-scenario |
| [design.md](design.md) | 薄分发设计与三段管道废弃史、两条物理路径、Primary 四种选择、委派/规划语义、动态规格、流式 phase、递归预算传递、Session Tree 镜像、与 vage 边界 |
| [models.md](models.md) | Task Plan、Plan Step、Dynamic Agent Spec、Plan Workspace(引用 session 领域) |

## vv-prd 对照

- 模型:[../../../../vv-prd/models/core/planner/](../../../../vv-prd/models/core/planner/)(task-plan / plan-step / dynamic-agent-spec)、[../../../../vv-prd/models/core/workspace/model-plan-workspace.md](../../../../vv-prd/models/core/workspace/model-plan-workspace.md)
- 流程:[../../../../vv-prd/procedures/core/orchestration/procedure-orchestration.md](../../../../vv-prd/procedures/core/orchestration/procedure-orchestration.md)、[../../../../vv-prd/procedures/core/planner/](../../../../vv-prd/procedures/core/planner/)、[../../../../vv-prd/procedures/core/routing/procedure-routing.md](../../../../vv-prd/procedures/core/routing/procedure-routing.md)
- 字典:[../../../../vv-prd/dictionaries/core/dictionary-plan-status.md](../../../../vv-prd/dictionaries/core/dictionary-plan-status.md)、[../../../../vv-prd/dictionaries/core/dictionary-plan-step-status.md](../../../../vv-prd/dictionaries/core/dictionary-plan-step-status.md)
- 源码:`vv/dispatches/`(Dispatcher、Primary 中继、DAG 构建、动态代理、递归深度、phase tracker、Session Tree 镜像)

> 说明:`procedure-plan-generation.md` / `procedure-plan-execution.md` / `procedure-routing.md` 均已标 **Superseded**,其职责已并入 Primary 的 ReAct 循环。`procedure-orchestration.md` 仍描述旧三段管道(含 intent 识别、fast-path),其语义在当前实现中已被统一 Primary 取代;读取该文件时以本领域 spec / design 为准。
