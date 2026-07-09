# 0003 — 能力分级 ToolProfile

- **Status**: proposed
- **Date**: 2026-06-02

## Context

每个代理能用哪些工具需要明确管理。若在代理代码里逐个硬编码工具列表,新增工具要改多处,"某代理有什么权限"无法一句话陈述,也难以保证安全边界一致。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 每个代理硬编码工具列表 | 直观 | 新增工具改多处;权限分散难审计 |
| **声明式 ToolProfile(选定)** | 同一 Factory + 不同 profile 产出多种代理;能力维度归类 | 需维护能力→工具映射表 |
| 运行期动态授权 | 灵活 | 难静态审计;易出现"半就绪"权限 |

## Decision

代理可用工具不在调用点硬编码,而由代理描述符(AgentDescriptor)声明的 **ToolProfile** 决定。四档预设:

| Profile | 能力 | 典型代理 |
|---------|------|---------|
| Full | Read + Write + Execute + Search | Coder |
| Review | Read + Search + Execute | Reviewer |
| ReadOnly | Read + Search | Researcher / Primary |
| None | ∅ | Planner / Fallback Primary |

装配阶段把 Capability 翻译为具体工具(`registerCapabilityTools`)。预设之外允许自定义(动态规格场景),但日常优先映射四档。

## Consequences

- ✅ 新增工具只需归类到能力维度,不必逐个代理改。
- ✅ "某代理有什么权限"可由 profile 一句话陈述,便于安全审计。
- ✅ orchestration 的动态代理复用同一模型(`ProfileByName` 解析 `full/read-only/review/none`)。
- 能力鸿沟是有意设计:researcher/reviewer 无写工具,迫使一次 mutation 永远经过 Coder。

## Compliance

- 代码:禁止在调用点用 if 分支临时给代理加/减工具;能力变更必须经描述符的 ToolProfile。
- 测试:每个 profile 产出的工具集需断言正确(如 researcher 无 write/bash)。

## References

- `doc/domains/core/agents/`、`doc/domains/core/tools/`
- 代码:`vv/registries/tool_access.go`、`vv/agents/`
