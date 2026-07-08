# mcp — Domain Spec

## Overview

mcp 领域定义 vv 的 **MCP 服务端形态**(`vv --mode mcp`):把 vv 自身暴露为一个 MCP(Model Context Protocol)server,让上游 LLM IDE(Claude Desktop / Cursor / Cline / Goose / MCP Inspector)以"调用工具"的方式驱动 vv 的专家代理。

核心命题是 **角色翻转**:在 CLI/HTTP 模式里 vv 是"应用"、终端用户面对它;在 MCP 模式里 vv 是"工具源",上游 LLM 面对它,自行决定何时把编码任务派给 `coder`、研究任务派给 `researcher`、评审派给 `reviewer`。每个被暴露的 dispatchable 代理变成一个 MCP tool,接 `{"input": "<自由文本任务>"}`,返回该代理 ReAct 循环的最终文本回答。

**范围**:暴露面的选择(白名单 + expose_dispatcher 开关)、两种传输(stdio / Streamable HTTP)、网络暴露的安全门(非 loopback 默认拒绝 + Bearer auth)、MCP 模式下安全层的持续生效与凭据过滤在 server 边界的扩展。

**边界**:本领域**不实现** MCP 协议本身(JSON-RPC、Streamable HTTP、session、DNS rebinding 防护均来自 MCP Go SDK 与 vage `mcp/server`),**不定义**被暴露的代理(归 [agents](../agents/))与工具(归 [tools](../tools/)),只负责 vv 侧的暴露接线。安全约束的量化指标见 [../../../non-functional/security.md](../../../non-functional/security.md);术语见 [../../../glossary.md](../../../glossary.md)。

## Core entities

| 实体 | 职责 | 详见 |
|------|------|------|
| MCP Server 配置(`mcp.server.*`) | 控制暴露面与传输的声明式配置:transport / addr / auth_token / agents 白名单 / expose_dispatcher / session_timeout | [models.md](models.md) |
| 暴露工具描述符(Exposed Tool) | 一个被暴露代理在 MCP 工具列表里的呈现:工具名 = 代理 ID,入参 `{input}`,出参 = 代理最终文本 | [models.md](models.md) |
| 传输配置(Transport) | 解析后的传输形态:Kind(stdio / http)+ Addr + Loopback 标志 | [models.md](models.md) |

被暴露代理的能力面(coder=Full / researcher=ReadOnly / reviewer=Review)由 [agents](../agents/) 领域定义,本领域不复述;完整配置字段权威清单引用 [application-mcp.md](../../../../vv-prd/applications/mcp/application-mcp.md)。

## 暴露工具面

| MCP 工具名 | 来源 | 暴露条件 |
|-----------|------|---------|
| `coder` | registry `coder` 代理(Full:read·write·execute·search) | 默认(在白名单内或白名单为空) |
| `researcher` | registry `researcher` 代理(ReadOnly:read·search) | 同上 |
| `reviewer` | registry `reviewer` 代理(Review:read·search·execute) | 同上 |
| `dispatcher` | Dispatcher(统一 Primary 入口) | **仅当** `expose_dispatcher=true` |

调用语义:上游传入任务描述 → vv 在 server 端构造一次代理 Run → 代理在自己的 ReAct 循环中跑(仍是完整能力,可调用它自身的底层工具)→ 最终回答以 tool output 返回。上游看到的是"我调用了一个工具",不是"我对话了一个代理",故会话状态**不跨多次 tool 调用累加**(除非上游显式管理 session id)。

## Business rules(不变量)

| ID | 规则 | 说明 |
|----|------|------|
| MCP-R1 | 暴露面由白名单 + expose_dispatcher 控制 | `mcp.server.agents` 为空 = 暴露全部 dispatchable 代理;非空 = 仅暴露列出的代理 ID(列了未注册的 ID → 启动期报错)。Dispatcher 默认**不暴露**,仅 `expose_dispatcher=true` 时追加为 `dispatcher` 工具。 |
| MCP-R2 | 非 loopback 默认拒绝 | transport=http 且 addr 绑定非 loopback 主机(含裸 `:port` 监听全部网卡)时,未设 `auth_token` 则**启动期拒绝**。该校验在 config-time(`ValidateMCPServer`)与 startup-time(`ResolveTransport`)双层设防,直接构造配置的 embedder 也无法绕过。量化见 [security.md](../../../non-functional/security.md)「MCP 网络暴露」。 |
| MCP-R3 | Bearer 认证 + 常量时间比较 | 设了 `auth_token` 时,除 `GET /healthz`(返回 204、无需认证)外每个请求须带 `Authorization: Bearer <token>`,比较用常量时间(`crypto/subtle.ConstantTimeCompare`),杜绝时序侧信道。 |
| MCP-R4 | DNS rebinding 防护继承 SDK 默认 | loopback socket 上 Host 头为非 loopback 的请求返回 403(MCP Go SDK `DisableLocalhostProtection=false`),防止浏览器侧 DNS rebinding 攻打本地 server。 |
| MCP-R5 | ask_user 返回错误不挂起 | MCP 模式无终端、上游期望同步无打断的工具执行;`ask_user` 接 `NonInteractiveInteractor`——代理发起提问得到显式"无交互终端"错误,**绝不挂起**等待。 |
| MCP-R6 | 所有底层安全层继续生效 | 工作区 allow-list、bash 分级(Dangerous/Blocked 硬拒绝,非交互)、工具结果注入扫描在 MCP 模式与 HTTP 模式姿态**完全一致**。CLI 权限对话框**不安装**(无终端),`permissionState` 视为非交互。 |
| MCP-R7 | 凭据过滤扩展到 server 边界 | MCP 凭据扫描器除原有 client 出/入两点外,**新增 server 入(client→agent 入参)/ server 出(agent→client 结果)两点**,共 4 个扫描点。默认 `enabled=true, action=redact`;命中发 `EventMCPCredentialDetected` + `slog.Warn`,载荷只带掩码预览。量化见 [security.md](../../../non-functional/security.md)「MCP 凭据过滤」。 |
| MCP-R8 | 不暴露原始工具 | `bash` / `read` / `write` / `edit` / `glob` / `grep` **绝不**作为顶层 MCP 工具暴露——刻意保留代理的系统提示/护栏在回路里,防止上游绕过 dispatch 流水线直接操作文件系统。 |
| MCP-R9 | 无 MCP passthrough | vv 作为 MCP **客户端**连接的下游 server 的工具,**不**被中继(passthrough)为 vv 自己的 MCP 工具。vv 暴露的工具集 = 它自己的代理,边界清晰。 |
| MCP-R10 | 非 dispatchable 代理不暴露 | 与 AGENTS-R6 一致:planner 等 `Dispatchable=false` 的代理永不出现在 MCP 工具列表(白名单也只能选 dispatchable 代理 ID)。 |

> 注:工具名映射、各代理能力面、扫描规则集与默认动作等可从代码/security NFR 恢复的细节不在此复述——见 [design.md](design.md)、[agents](../agents/) spec、[security.md](../../../non-functional/security.md)。

## States & transitions

MCP server 是**无生命周期业务状态机**的长驻进程组件。startup 一次性:解析配置 → 校验网络暴露门 → 选择暴露代理 → 装凭据扫描器 → 注册代理 → 绑定传输,随后稳态服务。优雅停机:SIGINT/SIGTERM 取消 context,HTTP handler 调 `Server.Shutdown`,停机 goroutine 被正确 join(即便 `Serve` 意外返回也不泄漏)。每次 tool 调用产生独立的代理 Run,默认 **stateless**(可选 session 子系统,但价值有限,见 [design.md](design.md))。

## Domain events

本领域不发布自有业务事件。唯一与本领域强相关的事件是 `EventMCPCredentialDetected`(凭据扫描命中,在 4 个扫描点的 server 侧两点也会触发),其定义与消费归安全/[trace](../trace/) 领域;本领域只负责把扫描器接到 server 边界并把命中转写为 `slog.Warn`(载荷仅掩码预览)。

## Interactions

| 协作领域 | 关系 |
|----------|------|
| [agents](../agents/) | 上游:被暴露的对象。经 AgentLookup 取 dispatchable 代理列表;白名单按代理 ID 选取子集。 |
| [orchestration](../orchestration/) | 上游:Dispatcher 由它构造;`expose_dispatcher=true` 时把 Dispatcher 暴露为 `dispatcher` 工具。 |
| [configuration](../configuration/) | 上游:`mcp.server.*` 解析、`ValidateMCPServer` 启动期校验、`BuildMCPCredentialScanner` 构造、setup.Init 提供 AgentLookup + Dispatcher。 |
| [tools](../tools/) / 安全领域 | 横切:所有底层安全层(allow-list / bash 分级 / 注入扫描)在 MCP 模式继续生效;凭据过滤扫描器接到 MCP server 入/出边界(MCP-R7)。 |
| [http-api](../http-api/) | 平级:同为代理的对外暴露面(HTTP 子端点 vs MCP 工具),三模式互斥。 |

## Non-goals

- **不做多用户认证**:仅支持**单个共享 Bearer token**,无 OAuth / JWT / audience-scoped / 多租户隔离(P4-1 用户认证将取代)。
- **不做 MCP passthrough**:不把 vv 作为客户端连接的下游 server 工具中继出去(MCP-R9)。
- **不暴露原始工具**:`bash/read/write/edit/glob/grep` 绝不作为顶层 MCP 工具(MCP-R8)。
- **不实现 MCP 协议本身**:JSON-RPC / Streamable HTTP / session / DNS rebinding 防护来自 MCP Go SDK 与 vage。
- **不支持旧 HTTP+SSE 传输**:仅 Streamable HTTP(spec `2025-03-26`);旧 SSE 传输上游已废弃,刻意不支持。
- **不支持** Prompts / Resources / Elicitation / 动态 tool-list 变更通知。
- **不并行运行** CLI / HTTP / MCP:同一进程三模式互斥;MCP 模式拒绝 `-p`。

## Anti-scenario(绝不能发生)

- **非 loopback 绑定且无 token 绝不得启动**:配 `addr: 0.0.0.0:7801`(或裸 `:7801`)而未设 `auth_token` → 进程必须在启动期报错退出,**绝不**裸跑公网。这是本领域最硬的约束(MCP-R2),双层校验保证 embedder 也无法绕过。
- **ask_user 绝不挂起**:代理在 MCP 模式发起 `ask_user`,绝不阻塞等待一个不存在的终端,必须立即返回显式错误(MCP-R5)。
- **原始工具绝不暴露**:即便上游"只想直接读一个文件",也绝不把 `read` 暴露为顶层 MCP 工具——必须经代理(MCP-R8)。
- **凭据绝不明文落日志**:扫描命中事件与日志只带掩码预览(前若干字符 + `****`),绝不带凭据明文。

## Data dictionary

| 术语 | 语义类型 | 定义 |
|------|---------|------|
| MCP 模式 | 概念 | `vv --mode mcp`;把代理暴露为 MCP 工具,stdio 或 Streamable HTTP 传输(见 glossary) |
| 暴露工具(Exposed Tool) | 概念 | 一个被暴露 dispatchable 代理在 MCP 工具列表中的呈现;工具名 = 代理 ID,入参 `{input}` |
| transport | enum | 传输形态:`stdio`(默认)/ `http`(Streamable HTTP) |
| loopback 绑定 | 概念 | addr 主机为 `localhost` / 127.0.0.0/8 / `::1`;裸 `:port`(空主机)**不算** loopback |
| auth_token | text(secret) | 共享 Bearer token;非 loopback 绑定时**必填**;比较用常量时间 |
| 白名单(agents) | reference 集合 | `mcp.server.agents`:要暴露的代理 ID 列表;空 = 全部 dispatchable |
| expose_dispatcher | enum(bool) | 是否把 Dispatcher 暴露为顶层 `dispatcher` 工具,默认 false |
| MCP 凭据过滤 | 概念 | MCP I/O 边界扫描凭据/敏感字段;MCP 模式扩展到 server 入/出两点,共 4 点(见 glossary、security NFR) |
| 非交互 interactor | 概念 | `NonInteractiveInteractor`:代理发起提问时返回"无交互终端"错误而非挂起 |
| MCP passthrough | 概念(反向) | 把 vv 作为客户端连接的下游 server 工具中继出去——**本领域明确不做** |
