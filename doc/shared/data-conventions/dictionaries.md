# 共享数据字典

跨领域引用的枚举/字典集中索引于此。**完整枚举值清单**(每个枚举的所有取值与语义)是制品级细节,本文件只给索引与归属领域,引用而不复述。

| 字典 | 取值要点 | 归属领域 | 制品来源 |
|------|---------|---------|---------|
| Agent Type | task / orchestrator | agents, orchestration | `dictionary-agent-type.md` |
| Tool Source | 工具来源(内建 / MCP …) | tools | `dictionary-tool-source.md` |
| Tool Access Level | 动态子代理工具级别:full / read-only / review / none(对应 ToolProfile) | agents, orchestration | `dictionary-tool-access-level.md` |
| Bash Risk Tier | Safe / Caution / Dangerous / Blocked | tools | `dictionary-bash-risk-tier.md` |
| Tool-Result Injection Action | log / rewrite / block | tools | `dictionary-tool-result-injection-action.md` |
| Permission Mode | default / accept-edits / auto / plan | cli | `dictionary-permission-mode.md` |
| Confirmation Action | Allow / Allow Always / Deny | cli | `dictionary-confirmation-action.md` |
| CLI Session Status | CLI 会话状态 | cli | `dictionary-cli-session-status.md` |
| CLI Message Role | 消息发送者角色 | cli | `dictionary-cli-message-role.md` |
| Run Mode | cli / http(/ mcp) | configuration | `dictionary-run-mode.md` |
| Memory Level | working / session / persistent | memory | `dictionary-memory-level.md` |
| Memory Namespace | 共享 vs 会话私有 | memory | `dictionary-memory-namespace.md` |
| Plan Status | 任务计划生命周期状态 | orchestration | `dictionary-plan-status.md` |
| Plan Step Status | 计划步骤生命周期状态 | orchestration | `dictionary-plan-step-status.md` |
| Task Status | async 任务状态 | http-api | `dictionary-task-status.md` |
| Budget Scope | run / session / daily | budget | `dictionary-budget-scope.md` |
| Evaluator Name | latency / cost / contains / llm_judge (+ exact_match / tool_call) | eval | `dictionary-evaluator-name.md` |

## 业务语义类型约定

spec/design 中描述数据用 **业务语义类型**(text / number / date / enum / reference / duration / bytes),而非数据库技术类型(varchar / int)。物理类型留给代码与 schema。
