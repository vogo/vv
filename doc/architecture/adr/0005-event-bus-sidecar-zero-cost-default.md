# 0005 — 事件总线旁路订阅 + 零成本默认

- **Status**: proposed
- **Date**: 2026-06-02

## Context

vv 有多个观测/持久化子系统:trace JSONL、session 事件持久化、budget 计数、debug、session tree auto-enable 计数。它们都需要观察代理执行过程。若各自插桩主路径,会增加主请求延迟,且每加一个观测维度都要改主路径。多数子系统默认不需要,默认安装应快且简单。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 各子系统直接插桩主路径 | 直接 | 主路径延迟随子系统增加;改主路径才能加维度 |
| **统一事件总线 + 旁路订阅(选定)** | 主路径只发事件;加维度=加订阅者;未启用零开销 | 订阅者需容忍彼此延迟(异步 hook 缓解) |
| 每子系统独立事件管道 | 隔离 | 维护多套订阅模型 |

## Decision

复用 vage 的统一事件总线。代理执行发出标准 `schema.Event`(agent/iteration/tool/llm/phase/guard/budget…)。trace / session / budget / tree 等都作为**旁路订阅者**挂载,主路径只发事件、不等待落盘。配套 **零成本默认路径**:子系统 `enabled` 为 nil/false 时**不构造、不挂事件、不起 goroutine、不建目录**。trace 异步落盘,满通道非阻塞丢弃 + `slog.Warn`。

## Consequences

- ✅ 主路径不被观测污染;新增观测维度只需加一个订阅者。
- ✅ 默认安装零额外开销与延迟。
- ⚠️ 异步落盘需在优雅关闭时主动 flush(独立 3s 上下文,见 availability NFR)。
- ⚠️ 所有订阅者共用一条总线,须容忍彼此延迟(异步 hook 缓解)。

## Compliance

- 代码:观测/持久化逻辑不得插入主请求同步路径;必须经事件总线订阅。
- 测试:断言子系统未启用时不构造任何 goroutine/句柄/目录。

## References

- `doc/domains/core/trace/`、`doc/non-functional/{performance,availability}.md`
- `doc/architecture/architecture.md` § 零成本默认
- 代码:`vv/traces/`、`vv/hooks/`、`vv/debugs/`、`vv/setup/`
