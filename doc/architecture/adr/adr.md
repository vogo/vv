# ADR 编号与写作约定

本目录记录 vv 的 **架构决策记录(Architecture Decision Records)**。

## 约定

- 文件名:`NNNN-kebab-title.md`,`NNNN` 零填充递增(`0001`、`0002`…)。
- 每个 ADR 只覆盖 **一个** 架构决策;相关但可分离的决策写两个 ADR。
- 必含章节:Status / Date / Context / Options considered / Decision / Consequences / Compliance / References。
- Status:`proposed` / `accepted` / `deprecated` / `superseded`。
- **ADR 永不删除**:废弃时标记 `deprecated` 或 `superseded`,链接到替代者,移入 `deprecated/`。
- **写入前需人工评审**:不直接落盘 ADR;先呈递草稿,获批后以 `Status: proposed` 写入,用户确认决策后才升 `accepted`。

## ADR 索引

下列 ADR 已起草并写入,**当前均为 `Status: proposed`**(草稿态)。下列架构决策已在代码中落地多时,经干系人确认后应逐个提升为 `accepted`。

| 编号 | 标题 | 状态 |
|------|------|------|
| [0001](0001-unified-primary-front-door.md) | 统一前门 Primary 替代三段管道 | proposed |
| [0002](0002-recursion-depth-hard-valve.md) | 递归深度硬阀门 + Fallback Primary | proposed |
| [0003](0003-capability-tiered-toolprofile.md) | 能力分级 ToolProfile | proposed |
| [0004](0004-shared-root-session-directory.md) | 共根会话目录 | proposed |
| [0005](0005-event-bus-sidecar-zero-cost-default.md) | 事件总线旁路订阅 + 零成本默认 | proposed |
| [0006](0006-pure-go-sqlite-memory-backend.md) | 纯 Go SQLite 记忆后端 | proposed |
| [0007](0007-session-private-memory-access-control.md) | 持久记忆会话私有访问控制 | proposed |
| [0008](0008-budget-hard-valve-soft-warn.md) | 预算硬阀门 + 软告警 | proposed |

> **下一步**:这些 ADR 描述的决策已在代码中既成事实。请逐个复核 → 确认后把对应文件的 `Status` 改为 `accepted`。若某条决策需重新讨论,在评审中提出。
