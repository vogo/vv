# tools 领域实体模型(models)

> 业务语义类型(text / boolean / enum / number / structured / reference)。完整字段定义见 [../../../../vv-prd/models/core/tools/model-tool.md](../../../../vv-prd/models/core/tools/model-tool.md);业务行为见 [spec.md](spec.md);机制见 [design.md](design.md)。枚举值清单回链 vv-prd/dictionaries/。

## Tool / ToolDef

**用途**:注册到工具注册表的一个可调用能力;代理在 ReAct 循环中按 `description` 选择并以 `parameters` 调用。

| 属性 | 类型 | 必填 | 说明 |
|------|------|------|------|
| name | text | 是 | 唯一工具名(bash/read/write/edit/glob/grep/web_fetch/web_search/ask_user/todo_write) |
| description | text | 是 | 给 LLM 看的能力描述,用于工具选择 |
| parameters | structured | 是 | JSON Schema 入参定义,每个工具不同 |
| source | enum([tool-source](../../../../vv-prd/dictionaries/core/dictionary-tool-source.md)) | 是 | local(内建)/ mcp / agent;MVP 全为 local |
| read_only | boolean | 是 | 是否仅读取观察而不改状态;决定 plan 模式可用性(TOOLS-R1) |

**关系**:与 Agent 多对多(经注册表);由 Configuration 提供构造参数(超时、工作目录)。

## ToolProfile

**用途**:一组具名能力;代理通过持有一个 profile 间接获得工具集(TOOLS-R2)。

| 属性 | 类型 | 必填 | 说明 |
|------|------|------|------|
| name | text | 是 | full / review / read-only / none |
| capabilities | enum 集合 | 是 | {read, write, execute, search} 的子集 |

四档预设及能力→工具映射见 [design.md](design.md) § 能力分级。

**关系**:被 AgentDescriptor(agents 领域)持有;`ProfileByName` 供动态代理(orchestration 领域)按 [tool-access-level](../../../../vv-prd/dictionaries/core/dictionary-tool-access-level.md) 解析;`BuildRegistry` 据此产出 `tool.Registry`。

## Bash 分级结果

**用途**:命令分类器对一条 bash 命令的判定;调制确认流。

| 属性 | 类型 | 说明 |
|------|------|------|
| tier | enum([bash-risk-tier](../../../../vv-prd/dictionaries/core/dictionary-bash-risk-tier.md)) | safe / caution / dangerous / blocked;整条命令取所有子命令最大档 |
| matched_rules | reference 集合 | 命中的规则(默认库 + 用户 YAML 扩展) |
| sub_commands | text 集合 | 按 `;`/`&&`/`||`/`$(...)`拆解后的子命令 |

**关系**:输入是 bash 工具的 `command` 入参;输出驱动权限模型与 BashTool 内部硬拒绝(TOOLS-R4)。

## 注入扫描结果

**用途**:工具结果注入扫描对一个工具返回文本的判定。

| 属性 | 类型 | 说明 |
|------|------|------|
| rule_hits | reference 集合 | 命中的规则名(20 规则包),含 `__truncated` 观测标记 |
| severity | enum | Low / Medium / High(命中规则的最高档) |
| action | enum([tool-result-injection-action](../../../../vv-prd/dictionaries/core/dictionary-tool-result-injection-action.md)) | log / rewrite / block;High 达 `block_on_severity` 无条件 block |
| direction | enum | guard.DirectionToolResult(固定) |
| snippet | text | 命中片段(观测用) |

**关系**:输入是 `ContentPart{Type:"text"}`;输出经 `EventGuardCheck` 上报(TOOLS-R5)。

## 凭据扫描结果

**用途**:credscrub Scanner 对 MCP I/O 一段内容的判定。

| 属性 | 类型 | 说明 |
|------|------|------|
| credential_type | enum | AWS / GitHub / Slack / JWT / PEM / Stripe / Google / OpenAI / Bearer / aws_secret_key / generic_api_key / JSON 字段名 |
| masked_preview | text | 前 4 字符 + `****`;**绝不含明文**(TOOLS-R6) |
| scan_point | enum | client 出站参数 / client 入站结果 / server 入站参数 / server 出站结果(4 点) |
| action | enum | log / redact(`[REDACTED:<type>]`)/ block |
| truncated | boolean | 内容超 `MaxScanBytes`(256 KiB)被截断 |

**关系**:经 `EventMCPCredentialDetected` 上报;仅 MCP 协议路径产生。

## Todo 列表项

**用途**:`todo_write` 维护的会话级检查清单项(TOOLS-R8)。

| 属性 | 类型 | 说明 |
|------|------|------|
| id | text | 服务端分配的稳定 `todo_<N>`,单调;LLM 可复用以保持项稳定 |
| content | text | 任务描述 |
| status | enum | pending / in_progress / completed;全局 in_progress ≤ 1 |

**列表级聚合(Snapshot)**:

| 属性 | 类型 | 说明 |
|------|------|------|
| session_id | reference | 隔离键;同会话共享一张表 |
| version | number | 严格单调;可作 SSE 幂等键 |
| items | Todo 列表项 集合 | 单次 ≤ 100 项;`null`/`[]` 均为清空 |

**关系**:进程级 `todo.Store` 按 session_id 隔离,仅内存、重启清空;与 `memory.Store` 正交;成功写入发 `EventTodoUpdate`(全快照)。
