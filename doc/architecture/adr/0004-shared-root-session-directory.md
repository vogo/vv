# 0004 — 共根会话目录

- **Status**: proposed
- **Date**: 2026-06-02

## Context

vv 把三个 vage 子系统组合到会话语境:Session(元数据 + 事件流)、Plan Workspace(plan.md + notes/)、Session Tree(结构化目标-子任务图)。三者各有磁盘状态。若各自独立存储,删除会话时需跨子系统协调一致性,易留孤儿状态。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 三子系统各自独立目录 | 解耦 | 删除需跨子系统协调;易留孤儿 |
| **共用一个会话目录根(选定)** | 删除一次递归即清全部;一致性自动达成 | 三者生命周期被绑定(可接受) |
| 单一数据库表 | 查询方便 | 与 vage 现有文件存储不符;增复杂度 |

## Decision

Session / Plan Workspace / Session Tree **共用一个会话目录根**。`DELETE /v1/sessions/{id}` 一次目录递归删除即清掉全部三套子系统状态。启用关系强校验:Session 默认开(关则三者全关);Plan Workspace 跟随 Session 不可单独控;Session Tree 默认关,显式开 + Session 必须开,在装配阶段强校验(配置错误启动期暴露)。

## Consequences

- ✅ 删除一致性自动达成,无孤儿状态。
- ✅ 配置错误(如开 Tree 关 Session)启动期报错而非沉默忽略。
- ⚠️ 三子系统生命周期绑定:不能单独保留 Tree 而删 Session。
- id-only 恢复:MVP 复用 id 让记忆/plan/tree 共目录,但不重放对话历史(checkpoint+replay 在路线图)。

## Compliance

- 代码:Workspace/Tree 必须挂在 Session 目录根下;装配阶段校验启用依赖。
- 测试:`DELETE` 后断言三套子系统状态全清(见 testing-strategy)。

## References

- `doc/domains/core/session/`
- 代码:`vv/setup/`(会话装配)
