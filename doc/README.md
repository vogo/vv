# vv 设计文档索引

`vv` 是基于 vage 框架与 aimodel SDK 构建的代理应用。本目录是 vv 的 **设计文档集**，关注架构、核心流程、模式选择与子系统边界，不复述代码实现。

## 文档列表

| 文档 | 主题 |
|------|------|
| [architecture.md](architecture.md) | 整体架构：分层、依赖关系、运行模式、设计原则 |
| [main.md](main.md) | 启动入口与生命周期：模式分派、互斥规则 |
| [setup.md](setup.md) | 初始化装配：组件构造顺序、可选子系统挂载策略 |
| [dispatch.md](dispatch.md) | 分发器与 Primary Assistant：请求生命周期、递归预算 |
| [agents.md](agents.md) | 代理设计：Primary 与三类专家、能力分工 |
| [registries.md](registries.md) | 注册表与能力分级：ToolProfile 模型 |
| [tools.md](tools.md) | 工具集合：归类、装饰链、安全约束 |
| [configs.md](configs.md) | 配置体系：来源优先级、子系统开关 |
| [cli.md](cli.md) | CLI：交互式 TUI、单提示模式、权限模式 |
| [httpapi.md](httpapi.md) | HTTP：REST/SSE 入口、子系统路由策略 |
| [mcp.md](mcp.md) | MCP：把代理暴露给上游 LLM IDE |
| [memories.md](memories.md) | 记忆系统：三层视图、命名空间策略 |
| [session.md](session.md) | 会话/Plan Workspace/Session Tree：共根目录与启用关系 |
| [observability.md](observability.md) | 可观测性：debug/trace/budget/hook 总览 |
| [eval.md](eval.md) | 评测子系统：数据集模型与执行策略 |

## 阅读路径

- 想理解 vv 整体先看 [architecture.md](architecture.md)。
- 想跟一次请求走通主调用链：[main.md](main.md) → [setup.md](setup.md) → [dispatch.md](dispatch.md) → [agents.md](agents.md)。
- 想理解可选子系统之间的关系：[session.md](session.md) → [observability.md](observability.md)。

## 约定

- 中文为主，英文术语保留原样。
- 文档不含函数/字段/源码行号引用；如需对照代码请直接读 `vv/CLAUDE.md` 中的快速索引。
- 与 vage 子系统耦合的概念会回链到 `vage/.doc/*.md`。
