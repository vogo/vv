# mcp 领域总览

- **领域名 / 组**:mcp / core
- **一句话职责**:把 vv 自身作为 **MCP(Model Context Protocol)服务端** 暴露给上游 LLM IDE(Claude Desktop / Cursor / Cline / Goose / MCP Inspector)——将每个 dispatchable 专家代理(coder / researcher / reviewer)、可选 Dispatcher 映射为 MCP tool,使 vv 成为"被别的代理调用"的工具源(agent-by-agent)。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:
  - [agents](../agents/)(被暴露的对象就是 dispatchable 专家代理;非 dispatchable 代理 planner 永不出现在 MCP 工具列表——见 AGENTS-R6)。
  - [configuration](../configuration/)(`mcp.server.*` 配置解析、启动期校验 `ValidateMCPServer`、凭据扫描器 `BuildMCPCredentialScanner` 构造、setup.Init 提供 AgentLookup + Dispatcher)。
  - [tools](../tools/) / 安全领域(所有底层安全层——工作区 allow-list、bash 分级、注入扫描——在 MCP 模式继续生效)。
- **API exposure**:true —— 本领域**就是**一种对外接口:通过 MCP 协议(JSON-RPC),传输为 stdio(默认)或 Streamable HTTP(spec `2025-03-26`)。非 REST,故无 OpenAPI 契约;协议契约由 MCP 标准 + 暴露工具描述符承载(见 [models.md](models.md))。

## 边界声明

- **不实现 MCP 协议本身**:JSON-RPC 编解码、Streamable HTTP handler、session 管理、DNS rebinding 防护均由 MCP Go SDK(`github.com/modelcontextprotocol/go-sdk`)与 vage 的 `mcp/server` 包提供。本领域负责的是 **vv 侧的接线**:选择暴露哪些代理、构造传输、强制网络暴露安全门、把凭据扫描器装到 server 边界。
- **不定义代理与工具**:专家代理描述符归 [agents](../agents/),Primary/Dispatcher 归 [orchestration](../orchestration/),工具实体与护栏归 [tools](../tools/)。本领域只把它们 **暴露**。
- **CLI / HTTP / MCP 三模式互斥**:同一进程只能运行一种(见 [spec.md](spec.md) Non-goals)。

## 关联候选 ADR

- **MCP 网络暴露安全门(非 loopback 默认拒绝)**:任何非 loopback 绑定(含裸 `:port`)在未设 `mcp.server.auth_token` 时**启动期拒绝**;该校验在 config-time(`ValidateMCPServer`)与 startup-time(`ResolveTransport`)**双层**设防,使直接构造配置的 embedder/测试也无法静默绕过。取舍详见 [design.md](design.md)「网络暴露安全门」与 [../../../architecture/adr/adr.md](../../../architecture/adr/adr.md)(尚待评审)。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、不变量(MCP-R*)、传输对比、领域事件、Non-goals、Anti-scenario、数据字典 |
| [design.md](design.md) | 代理暴露设计、两种传输对比、网络暴露安全门、白名单/expose_dispatcher、ask_user 非交互 interactor、凭据过滤在 server 边界、安全层复用、技术取舍 |
| [models.md](models.md) | MCP Server 配置、暴露工具描述符、传输配置 |

## vv-prd 对照

- 应用规格:[../../../../vv-prd/applications/mcp/application-mcp.md](../../../../vv-prd/applications/mcp/application-mcp.md)
- 安全 NFR:[../../../non-functional/security.md](../../../non-functional/security.md)(「MCP 网络暴露」+「MCP 凭据过滤」)
- 术语:[../../../glossary.md](../../../glossary.md)(MCP 模式 / MCP 凭据过滤)
