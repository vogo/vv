# memory 领域总览

- **领域名 / 组**:memory / core
- **一句话职责**:三层记忆(working / session / persistent)的管理与持久化,叠加会话私有访问控制不变量。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:configuration(持久化后端、目录、三层 manager 由装配中心构造)。被 cli / http-api 以 user-path 管理,被 agents 以 agent-path 读写。
- **API exposure**:true(经 [http-api](../http-api/http-api-overview.md) 的 `/v1/memory/*` 暴露共享 namespace 的 CRUD;CLI `/memory` 走同一 user-path 但非 HTTP)

## 关联 ADR

| ADR | 主题 | 状态 |
|-----|------|------|
| 0006 | 持久记忆后端:默认 `file`(JSON-per-entry),opt-in `sqlite`(单 `memory.db`、WAL、纯 Go `modernc.org/sqlite` 以保 `CGO_ENABLED=0`),file→sqlite 不自动迁移 | 候选(planned) |
| 0007 | 会话私有访问控制:agent-path 经 `WithSessionID` 绑定会话,user-path 经 `WithUserPath` 仅限共享 namespace,违反 surface 为 `ErrSessionForbidden` | 候选(planned) |

> ADR 文件尚未落盘;编号在本领域预占。落盘前须经 § 修订程序(constitution)双签——§ 0006 触碰技术栈基线(§ 2)、§ 0007 触碰数据基线(§ 4)。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、三层职责边界、不变量(MEM-R*)、状态迁移、Domain events、Non-goals、Anti-scenario |
| [design.md](design.md) | 三层视图、namespace 策略、磁盘布局、访问控制实现、file vs sqlite 后端、压缩机制、生命周期 |
| [models.md](models.md) | Memory Entry、Session Memory、Vector Store、Vector Document 实体模型 |

## vv-prd 对照

- 模型:[../../../../vv-prd/models/core/memory/](../../../../vv-prd/models/core/memory/)(model-memory-entry / model-session-memory / model-vector-store / model-vector-document)
- 流程:[../../../../vv-prd/procedures/core/memory/](../../../../vv-prd/procedures/core/memory/)(procedure-persistent-memory-management / procedure-session-memory-management)
- 字典:[dictionary-memory-level](../../../../vv-prd/dictionaries/core/dictionary-memory-level.md)、[dictionary-memory-namespace](../../../../vv-prd/dictionaries/core/dictionary-memory-namespace.md)

## 关联不变量

- 宪法 [§ 4 数据与一致性基线](../../../constitution.md):会话私有 namespace 条目绑定 `session_id`,跨会话不可读/写/删;CLI/HTTP user-path 仅限共享 namespace。
- 术语见 [../../../glossary.md](../../../glossary.md)「记忆与会话」小节。
