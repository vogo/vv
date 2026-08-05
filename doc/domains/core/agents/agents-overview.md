# agents 领域总览

- **领域名 / 组**:agents / core
- **一句话职责**:正交能力维度(ToolProfile / Skill / ContextSource)与预制组合(coder / researcher / reviewer)的描述符声明与 Factory+profile 装配——把"执行者能用哪些工具""按什么纪律产出""读什么只读上下文"建模为声明式数据结构,使新任务形态只需声明一个组合,而不是新增一个角色类型。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:
  - [tools](../tools/)(ToolProfile 的 Capability 在装配阶段翻译为具体工具集;`ask_user` / `todo_write` 注入也由 tools 领域承载)。
  - [configuration](../configuration/)(注册表的填充、工具集构造参数、PathGuard/护栏、Factory 依赖均来自装配中心;注册表每次启动构造一次)。
- **API exposure**:false(本领域不直接暴露端点;dispatchable 代理经 [http-api](../http-api/) 注册为子端点、经 [mcp](../mcp/) 暴露为 MCP 工具,但路由/暴露逻辑归那两个领域)。

## 边界声明

- **不含编排**:Primary / Fallback Primary 的构造、Dispatcher 转发、递归阀门、worker 派生入口(`spawn_worker`)、DAG 规划与执行均属 [orchestration](../orchestration/) 领域。本领域只提供编排所消费的**能力维度、预制组合描述符与工厂**。Planner 描述符虽登记在注册表内,但它的提示词消费与规划语义归 orchestration。
- **不自实现工具与代理循环**:TaskAgent(ReAct 循环)、ContextBuilder、记忆抽象、工具实体全部来自 vage;本领域负责的是"声明能力维度 + 按 profile 选工具 + 用 functional options 装配 TaskAgent"。
- **用户不能定义代理类型 / skill / context source**:三者集合都是启动期内置常量,非运行期可配置项(见 spec.md Non-goals)。

## 关联候选 ADR

- **0003 能力分级 ToolProfile**:代理可用工具不硬编码在调用点,而由 ToolProfile(Full / Review / ReadOnly / None)决定;新增工具按 Capability 归类后,所有匹配 profile 的执行者自动获得。ToolProfile 现在是更大的正交能力模型中的工具维度底座。详见 [design.md](design.md)「技术取舍」与 [../../../architecture/adr/adr.md](../../../architecture/adr/adr.md)(尚待评审)。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、不变量(AGENTS-R*)、预制组合表、领域事件、Anti-scenario、数据字典 |
| [design.md](design.md) | 能力维度与预制组合、ToolProfile 四档模型、Skill / ContextSource、注册表与描述符、Factory+profile 装配、ask_user/todo_write 注入、Guard/HookManager 注入、技术取舍 |
| [models.md](models.md) | AgentDescriptor、AgentType、ToolProfile/ToolCapability、Skill、ContextSource、预制组合配置 |

## 关联文档

- 术语:[../../../glossary.md](../../../glossary.md)
