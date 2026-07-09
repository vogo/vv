# cli 领域总览

- **领域名 / 组**:cli / core
- **一句话职责**:vv 的默认交互界面——基于 Bubble Tea/huh 的交互式 TUI,叠加四档权限模式与工具确认对话框,把分发器的流式事件渲染为终端对话。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:
  - [orchestration](../orchestration/orchestration-overview.md):用户消息经 in-process 直接调用分发器(Primary),无 HTTP 开销;TUI 消费其流式事件(text/tool/phase/sub-agent)。
  - [cost-tracking](../cost-tracking/cost-tracking-overview.md):状态栏实时展示累计 cost / tokens,由 `llm_call_end` 事件驱动更新。
  - [memory](../memory/memory-overview.md):`/memory` 命令以 user-path 管理共享 namespace;会话级 Session Memory 承载上下文压缩。
  - [budget](../budget/budget-overview.md):`/budget` 命令展示预算用量;预算告警以 toast 风格提示。
  - [configuration](../configuration/configuration-overview.md):权限模式初值、压缩阈值、`ask_user_timeout` 等由装配中心注入。
- **API exposure**:false(终端 UI,无网络接口)。`-p` 单提示与 `-eval` 评测同属 CLI 系运行模式,亦无网络接口。

## 关联 ADR

| ADR | 主题 | 状态 |
|-----|------|------|
| — | 权限模式四档语义(default / accept-edits / auto / plan)与 Allow Always 会话内作用域;bash 风险分级与权限模式正交;非交互模式降级为拒绝 | 未立项(行为已稳定,载于本领域 spec/design) |

> 本领域当前无独立 ADR 文件。权限语义的跨切面取舍记录在 [spec.md](spec.md) 的 CLI-R* 规则与 [design.md](design.md);若日后需冻结为决策记录,在 `architecture/adr/` 预占编号并经宪法修订程序签署。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、权限授权规则(CLI-R*)、状态机、Domain events、交互契约、Non-goals、Anti-scenario、数据字典 |
| [design.md](design.md) | 三入口(交互式 TUI / 单提示 / 评测)、权限模式机制、确认与 ask_user 对话框、流式渲染、状态栏、内建命令、上下文压缩通知、技术取舍 |
| [models.md](models.md) | CLI Session、CLI Message、权限模式状态 实体模型 |

## 关联不变量

- 权限模式与 bash 风险分级见 [glossary.md](../../../glossary.md)「工具与安全」「运行模式」小节。
- 工作区 allow-list 与 bash 分级属 [tools](../tools/tools-overview.md) 领域,本领域只消费其判定结果。
