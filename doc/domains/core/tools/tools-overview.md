# tools 领域总览

- **领域名 / 组**:tools / core
- **一句话职责**:工具集合的能力分级、装饰链装配与安全护栏(工作区隔离、bash 风险分级、工具结果注入扫描、MCP 凭据过滤)——代理实际可调用工具的唯一来源与边界。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:[configuration](../configuration/)(工具构造参数、allow-list、安全护栏开关均来自配置与装配中心)。
- **API exposure**:false(不对外暴露 HTTP/MCP 端点;原始工具 **不** 作为顶层 MCP 工具暴露,见 `../../../non-functional/security.md` § 安全的负空间)。

## 边界声明

- vv **不自实现工具实体**:bash / read / write / edit / glob / grep / web_fetch / web_search 全部来自 vage 的 `tool/<name>` 子包。本领域负责的是 **挑选、分级、装饰、加护栏**,而非工具内部逻辑。
- `ask_user` / `todo_write` 由 vv 在装配阶段注入到工具集。
- 委派工具(`delegate_to_*`)、规划工具(`plan_task`)、Plan Workspace / Session Tree 持久化工具属于 [agents](../agents/) 与 [orchestration](../orchestration/) 领域,本领域只声明它们在能力分级中的归属约束。

## 关联候选 ADR

- **0003 能力分级 ToolProfile**:代理工具集由 profile(Full / Review / ReadOnly / None)声明,不硬编码;新增工具按 Capability 归类后所有匹配 profile 的代理自动获得(见 `../../../architecture/adr/adr.md`,尚待评审)。
- 装饰链顺序约束(权限在内 / 截断居中 / 调试在外)——见 design.md「装饰链」,可考虑独立成 ADR。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、不变量(TOOLS-R*)、领域事件、Anti-scenario、数据字典 |
| [design.md](design.md) | 工具归类、装饰链、PathGuard、注入 Guard、credscrub Scanner、todo_write / web_search 设计、技术取舍 |
| [models.md](models.md) | Tool/ToolDef、ToolProfile、Bash 分级结果、注入/凭据扫描结果、Todo 列表项 |

## vv-prd 对照

- 模型:[../../../../vv-prd/models/core/tools/model-tool.md](../../../../vv-prd/models/core/tools/model-tool.md)
- 字典:[tool-source](../../../../vv-prd/dictionaries/core/dictionary-tool-source.md) · [tool-access-level](../../../../vv-prd/dictionaries/core/dictionary-tool-access-level.md) · [bash-risk-tier](../../../../vv-prd/dictionaries/core/dictionary-bash-risk-tier.md) · [tool-result-injection-action](../../../../vv-prd/dictionaries/core/dictionary-tool-result-injection-action.md) · [confirmation-action](../../../../vv-prd/dictionaries/core/dictionary-confirmation-action.md)
- 安全约束量化:[../../../non-functional/security.md](../../../non-functional/security.md)
