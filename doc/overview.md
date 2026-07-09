# Specs 知识库总览

本目录 `specs/` 是 **vv** 项目的规格知识库,以 DDD(领域驱动设计)视角组织,描述系统的 **意图、边界、不变量与跨切面决策**。

## 这份知识库的定位

| 文档集 | 视角 | 关注点 | 位置 |
|--------|------|--------|------|
| **specs/**(本目录) | 领域规格(WHAT / WHY) | 业务行为、边界、不变量、设计取舍 | 项目根 |
| **vv/**(源码 + `CLAUDE.md`) | 实现(HOW) | 代码、函数、装配 | `vv/` |
| **vv-changes/** | 变更记录 | 单次迭代的需求/设计/评审/测试 | 项目根 |

> `specs/` 写 **代码无法表达** 的内容:为什么存在、为什么这样设计、什么绝不能发生、跨多处代码的统一约定、稳定的业务不变量。

## 阅读路径

1. **理解项目** → [project.md](project.md):愿景、目标用户、范围边界。
2. **理解硬约束** → [constitution.md](constitution.md):任何 spec / 代码都不可越过的红线。
3. **理解架构** → [architecture/architecture.md](architecture/architecture.md):分层、统一前门、能力分级、递归预算、零成本默认。
4. **进入领域** → [domains/core/core-overview.md](domains/core/core-overview.md):13 个核心领域的索引与依赖。
5. **查术语** → [glossary.md](glossary.md):跨领域业务术语。

## 目录结构

```
specs/
├── overview.md              # 本文件
├── project.md               # 项目愿景、干系人、范围
├── constitution.md          # 全局硬约束
├── glossary.md              # 业务术语表
├── non-functional/          # 性能 / 安全 / 可用性
├── architecture/            # 全局架构 + ADR
├── api/                     # API 契约引用
├── shared/                  # 跨领域约定(数据字典、错误码、开发约定)
├── domains/core/            # 13 个 DDD 限界上下文
├── testing/                 # 测试策略
├── assets/                  # 图与资源
└── archived/                # 废弃内容(不删除,只归档)
```

## 写作规则

- **WHAT 与 HOW 分离**:`spec.md` 写业务行为,`design.md` 写技术实现。
- **就近引用代码**:能从代码读出的细节,链接到代码,不在此复述。
- **量化非功能需求**:不写"快""高""好",写具体数值。
- **指定负空间**:每个领域必须声明 non-goals(不做什么)与 anti-scenario(绝不能发生什么)。
- **就地增量更新**:改现有文件,不创建带版本号的新文件名。
- **归档而非删除**:废弃内容移入 `archived/`,ADR 移入 `architecture/adr/deprecated/`。
