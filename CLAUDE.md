# CLAUDE.md

本文件为 Claude Code（claude.ai/code）在 `vv/` 模块工作时的简要指引。

## 模块定位

`vv` 是基于 `vage` 框架与 `aimodel` SDK 构建的代理应用，提供 CLI / HTTP / MCP 三种运行模式。每次请求经由统一的 **Primary Assistant** 完成路由：直答、只读探查、委派专家、或 DAG 规划。

## 文档与源码对照

> 原 `doc/` 设计文档已迁移并重构为项目根的 **`specs/`** DDD 规格知识库。整体架构见 `../specs/architecture/architecture.md`,领域索引见 `../specs/domains/core/core-overview.md`。

每个主题对应一个 specs 领域与一个或多个源码目录:

| 主题 | 领域规格(`../specs/domains/core/`) | 源码目录 |
|------|---------|---------|
| 整体架构与设计原则 | `../specs/architecture/architecture.md` | — |
| 启动入口 / 装配中心 / 配置体系 | configuration/ | main.go、setup/、configs/ |
| 分发器与 Primary / 编排规划 | orchestration/ | dispatches/ |
| 代理设计 / 注册表与能力分级 | agents/ | agents/、registries/ |
| 工具集合与安全护栏 | tools/ | tools/、registries/ |
| CLI 模式 | cli/ | cli/ |
| HTTP 模式 | http-api/ | httpapis/ |
| MCP 模式 | mcp/ | mcps/ |
| 记忆系统 | memory/ | memories/ |
| 会话 / Plan Workspace / Session Tree | session/ | （由 setup/ 装配，复用 vage 子系统） |
| 成本追踪 / 预算执行 | cost-tracking/、budget/ | （LLM 中间件、setup/） |
| 可观测性(trace / debug / hooks) | trace/ | traces/、debugs/、hooks/ |
| 评测子系统 | eval/ | eval/ |

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
