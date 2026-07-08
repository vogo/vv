# 项目:vv — Vage Agent Application

## 一句话定位

vv 是构建在 **vage** 框架与 **aimodel** SDK 之上的、可生产部署的全能力 AI 代理应用 —— 一个面向开发者的智能编码助手与通用对话 AI,以"前门统一、内部分工"的方式处理每一次请求。

## 产品愿景

每一次用户请求都进入同一个 **Primary Assistant**,由它自行决定如何回应:直答、只读探查、委派给专家(coder / researcher / reviewer)、或在任务跨多个能力域时触发 DAG 多步规划。(闲聊与探查由 Primary 内联承担;早期的 `chat` / `explorer` 独立代理已移除。)三层记忆架构(working / session / persistent)让代理在请求内、对话间、会话间保留上下文。用户可通过 CLI 交互式 TUI(默认)、HTTP REST API 或 MCP 服务三种模式接入。

vv 的工程定位是 **挑选、组合、配置** vage 的可组合代理类型、工具系统、记忆管理、安全护栏与服务层,装配成单个可部署代理;它不向 vage 注入业务概念,避免基础库被特定应用形态绑死。

## 目标用户

| 用户群 | 特征 |
|--------|------|
| **开发者(User)** | 通过 CLI 或 API 进行 AI 辅助编码、文件操作、代码库导航的软件工程师 |
| **运维 / 部署者(Operator)** | 在生产环境部署、配置、运营 vv 服务的工程师 |
| **外部系统** | 以编程方式消费 vv HTTP API 的自动化系统与 CI/CD 流水线 |
| **上游 LLM IDE** | 通过 MCP 协议把 vv 代理当作工具调用的 Claude Desktop / Cursor / Cline / Goose 等客户端 |

完整角色与权限矩阵见 `vv-prd/architecture/roles.md`。

## 核心价值主张

- **统一前门 Primary Assistant**:单一入口,路由由模型以工具调用方式自行承担,而非前置意图分类管道。
- **智能任务编排**:简单任务直接委派,复杂任务分解为子任务 DAG,必要时动态创建临时专用代理。
- **三层记忆**:working(每请求)/ session(每对话,含摘要压缩)/ persistent(跨会话,文件或 SQLite)。
- **三种接口模式**:CLI 交互式 TUI、HTTP(sync/streaming/async)、MCP 服务(stdio / Streamable HTTP)。
- **安全护栏**:工作区 allow-list、bash 危险命令分级、工具结果注入扫描、MCP 凭据过滤。
- **成本与预算透明**:实时 token / 成本展示,session / daily 级硬上限,软告警阈值。
- **可观测**:可选 trace JSONL 落盘、debug 逐次 I/O 记录、统一事件总线旁路订阅。

## 范围边界

### 覆盖(Covers)

配置加载与 cwd 捕获、内建工具注册、四类专家代理 + Primary 编排、动态子代理、三层记忆、上下文压缩、token/成本追踪、session/daily 预算、CLI/HTTP/MCP 三模式、工作区与命令安全护栏、工具结果注入扫描、MCP 凭据过滤、trace 日志、离线评测。逐条清单见 `vv-prd/overview.md` § Covers。

### 不覆盖(Does Not Cover — 全局 Non-Goals)

- Web UI / 前端应用。
- 用户认证与授权、多租户隔离(MCP HTTP 仅支持单个共享 Bearer token)。
- 单进程内同时跑 CLI + HTTP + MCP(三者互斥)。
- 跨进程 / 跨机器的分布式代理执行;跨进程会话合并。
- 用户自定义代理类型或插件(Orchestrator 可内部动态创建临时代理,但用户不能定义)。
- 从对话自动抽取持久记忆(持久记忆由用户管理)。
- 记忆加密;trace 载荷的 PII / 密钥脱敏。
- 从 JSONL trace 恢复会话;OpenTelemetry / Langfuse 导出。
- 历史跨会话成本日志;货币换算(仅 USD)。

逐条清单与每条的理由见 `vv-prd/overview.md` § Does Not Cover。

## 干系人

| 干系人 | 关注 |
|--------|------|
| Operator | 部署可靠性、配置正确性、成本可控、安全默认 |
| Developer / User | 编码效率、上下文保真、交互流畅、可解释的代理行为 |
| 安全负责人 | 工作区隔离、命令安全、注入防御、凭据不泄露 |
| 框架维护者(vage) | vv 不污染 vage 抽象,边界清晰 |

## 关联文档

- 架构总览:[architecture/architecture.md](architecture/architecture.md)
- 领域索引:[domains/core/core-overview.md](domains/core/core-overview.md)
- 全局硬约束:[constitution.md](constitution.md)
- 制品目录:`vv-prd/`
