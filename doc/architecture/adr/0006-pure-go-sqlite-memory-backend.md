# 0006 — 纯 Go SQLite 记忆后端

- **Status**: proposed
- **Date**: 2026-06-02

## Context

持久记忆默认用 file 后端(JSON-per-entry)。大量条目下文件树的查询/管理成本上升,需要一个可选的结构化后端。同时 vv 必须保持 `CGO_ENABLED=0` 可构建(可移植、易交叉编译),这排除了基于 cgo 的 SQLite 驱动。

## Options considered

| 方案 | 优点 | 缺点 |
|------|------|------|
| 仅保留 file 后端 | 零依赖 | 大量条目下管理成本高 |
| cgo SQLite(`mattn/go-sqlite3`) | 成熟 | 破坏 `CGO_ENABLED=0` |
| **纯 Go SQLite(`modernc.org/sqlite`,选定)** | 保 CGO_ENABLED=0;单文件 DB | 纯 Go 实现性能略逊 cgo(可接受) |
| 外部 DB(Postgres 等) | 强大 | 部署复杂,违背单进程定位 |

## Decision

提供可插拔后端 `memory.backend`:默认 `file`(保留 JSON-per-entry 布局),opt-in `sqlite`。sqlite 后端把每条记忆存为单个 WAL 模式 `<memory_dir>/memory.db` 的一行,schema `user_version = 1`,每连接应用 `journal_mode=WAL`、`synchronous=NORMAL`、`busy_timeout=5000`、`foreign_keys=ON`。驱动用纯 Go 的 `modernc.org/sqlite` 以保 `CGO_ENABLED=0`。两后端满足同一 `memory.Store` 接口(会话绑定、namespace、TTL-on-read、legacy 记录语义一致)。配置加载期校验,未知 backend 拒绝。**不自动迁移**:file→sqlite 启新库,旧 JSON 树原样保留,可切回。

## Consequences

- ✅ 保持 `CGO_ENABLED=0` 构建。
- ✅ `setup.Init` 持有 DB 句柄,经 `InitResult.Shutdown` 关闭。
- ⚠️ 切后端不迁移历史数据(有意取舍,避免内置迁移复杂度;专门迁移器可后补)。
- daily 预算计数器尚未持久化到此 DB(P1-6 未做,可复用同一 DB 文件后补)。

## Compliance

- 代码:任何新 SQLite 用法必须用纯 Go 驱动;不得引入 cgo 依赖。
- 配置:未知 backend 值在 config-load 期拒绝。

## References

- `doc/domains/core/memory/`、`doc/constitution.md` § 2(CGO_ENABLED=0)
- 代码:`vv/memories/`、`vv/setup/`
