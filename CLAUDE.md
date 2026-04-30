# CLAUDE.md

本文件为 Claude Code（claude.ai/code）在 `vv/` 模块工作时的简要指引。

## 模块定位

`vv` 是基于 `vage` 框架与 `aimodel` SDK 构建的代理应用，提供 CLI / HTTP / MCP 三种运行模式。每次请求经由统一的 **Primary Assistant** 完成路由：直答、只读探查、委派专家、或 DAG 规划。

## 文档与源码对照

完整设计文档见 `doc/README.md`，每个主题对应一份文档与一个或多个源码目录：

| 主题 | 设计文档 | 源码目录 |
|------|---------|---------|
| 整体架构与设计原则 | doc/architecture.md | — |
| 启动入口与生命周期 | doc/main.md | main.go |
| 装配中心 | doc/setup.md | setup/ |
| 分发器与 Primary | doc/dispatch.md | dispatches/ |
| 代理设计 | doc/agents.md | agents/ |
| 注册表与能力分级 | doc/registries.md | registries/ |
| 工具集合 | doc/tools.md | tools/ |
| 配置体系 | doc/configs.md | configs/ |
| CLI 模式 | doc/cli.md | cli/ |
| HTTP 模式 | doc/httpapi.md | httpapis/ |
| MCP 模式 | doc/mcp.md | mcps/ |
| 记忆系统 | doc/memories.md | memories/ |
| 会话 / Plan Workspace / Session Tree | doc/session.md | （由 setup/ 装配，复用 vage 子系统） |
| 可观测性 | doc/observability.md | traces/、debugs/、hooks/ |
| 评测子系统 | doc/eval.md | eval/ |

## Build & Test

```bash
make build          # format → lint → test
make test           # go test ./... with coverage
make lint           # golangci-lint run
go test ./tools/ -run TestRegister_AllRegistered -v   # 单测示例
```

集成测试位于 `integrations/`，依赖环境变量 `VV_LLM_API_KEY`（或 `AI_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`）。单元测试无外部依赖。

依赖通过本地 `replace` 指令指向 `../aimodel` 与 `../vage`，兄弟模块的修改会立刻生效。

## 工程惯例

- 集成测试位于 `integrations/<group>_tests/<scenario>_tests/`；单元测试与源码同目录。
- 测试结束后清理构建产物。
- 工具/代理配置统一使用函数式选项（functional options）模式。
- 一切跨函数边界的操作都通过 `context.Context` 传递。
- 文档使用中文撰写，技术术语保留英文。
