# 0007 — 持久记忆会话私有访问控制

- **Status**: proposed
- **Date**: 2026-06-02

## Context

持久记忆既要支持跨会话共享的项目级知识(约定、用户偏好),也要让代理在单次会话内写临时事实而不污染全局、不被其他会话读取。无访问控制时,一个会话写的私有事实可能被另一个会话读到或覆盖,造成串话与数据正确性问题。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 所有 namespace 全局共享 | 简单 | 会话间串话;私有事实泄露 |
| **共享 + 会话私有双类 namespace(选定)** | 共享知识跨会话;私有事实隔离 | 需在 context 携带 session_id 并强制校验 |
| 每会话完全独立记忆 | 强隔离 | 失去跨会话共享项目知识的能力 |

## Decision

持久记忆 namespace 分两类:**共享**(`project` / `user` / `conventions` / `notes` / `default`,跨会话)与 **会话私有**(其余,绑定 `session_id`)。磁盘布局:共享 `<ns>/<key>.json`;私有 `session/<sid>/<ns>__<key>.json`。访问控制:

- 代理写当前会话(context 携带 `WithSessionID`),**不能读/改/删其他会话**的私有条目。
- CLI `/memory` 与 HTTP `/v1/memory/*` 走 **user-path**(`WithUserPath`),仅限共享 namespace,会话私有写返回 403;`Clear` 仅 user-path。
- legacy 记录(无 `session_id` 字段)可读,但在会话私有 namespace 受保护不被误覆盖。

违反一律 surface 为 `memories.ErrSessionForbidden`。

## Consequences

- ✅ 会话间私有事实隔离,消除串话。
- ✅ 共享知识仍跨会话可用。
- ⚠️ 一切记忆操作必须经 context 携带 session_id 或 user-path 标志。
- 不做记忆加密(out of scope)。

## Compliance

- 代码:记忆 Store 所有读写路径必须执行会话绑定校验;违反返回 `ErrSessionForbidden`。
- 测试(必测不变量):会话私有 namespace 跨会话不可读/写/删;user-path 仅限共享 namespace(见 testing-strategy)。

## References

- `specs/domains/core/memory/`、`specs/constitution.md` § 4
- 代码:`vv/memories/`
