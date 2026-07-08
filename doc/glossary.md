# 业务术语表(Glossary)

跨多个领域出现的业务术语集中定义于此,各领域 `spec.md` 引用而不重复定义。仅在某领域内部使用的术语,定义在该领域的 spec 数据字典中。枚举值的完整清单见 `shared/data-conventions/dictionaries.md`(回链 `vv-prd/dictionaries/`)。

## 核心角色与代理

| 术语 | 定义 |
|------|------|
| **vage** | vv 所依赖的底层代理框架,提供 TaskAgent、记忆抽象、ContextBuilder、工具实体、事件总线、session/workspace/tree 持久化、DAG orchestrate 等基础能力。 |
| **aimodel** | LLM SDK 层,把 OpenAI 与 Anthropic 等 provider 规一化为统一接口;承载 prompt caching 断点等跨 provider 能力。 |
| **Primary Assistant(Primary)** | 统一前门代理。每次请求的唯一入口,以 ReAct 循环自行选择直答 / 只读探查 / 委派 / 规划。 |
| **Fallback Primary** | 与 Primary 共享人格但 **无任何工具**、最大迭代 1 的实例;递归深度超限时切换到它,物理上消除再次递归的可能。 |
| **Dispatcher(分发器)** | 对外是单一 `agent.StreamAgent`,对内只做"转发到 Primary 或 Fallback"。不做意图分类、不做总结。 |
| **Dispatchable Agent(专家代理)** | 可被委派的子代理,实际注册三类:coder / researcher / reviewer。 |
| **Coder Agent** | 全工具访问(读写改 + bash + 搜索,ProfileFull)的编码专家。 |
| **Researcher Agent** | 只读工具(read/glob/grep,ProfileReadOnly)的代码库探查专家。 |
| **Reviewer Agent** | Review 能力档(read + search + execute,ProfileReview):可跑测试/lint,但无写工具。 |
| **内联直答(原 Chat / Explorer Agent 已移除)** | 闲聊由 Primary 无工具内联承担,探查由 Primary 用 read/glob/grep 自行完成;`chat` 与 `explorer` 已不再是独立注册的代理。 |
| **Planner** | ProfileNone(无工具)的内部分类代理,非 dispatchable,不对外暴露 `delegate_to_planner`。 |
| **Dynamic Agent(动态代理)** | 由 plan step 规格临时构造的临时代理:指定 base type + 工具子集 + 自定义系统提示。 |
| **Operator** | 部署、配置、运营 vv 的人。 |
| **User** | 通过 CLI/HTTP/MCP 与 vv 交互的人或外部系统。 |

## 编排与规划

| 术语 | 定义 |
|------|------|
| **ReAct 循环** | 代理的执行模型:每轮调用 LLM,从动作集合中选一个执行(直答/读取/委派/规划/记笔记)。 |
| **委派(Delegate)** | Primary 通过 `delegate_to_<agent>` 把任务交给某专家,递归深度 +1,结果以工具结果回到 Primary 并被 **折叠** 进最终回复。 |
| **规划(Plan)** | Primary 通过 `plan_task` 给出 goal + steps,触发 DAG 编排。 |
| **Task Plan / Plan Step** | 多步任务的有向无环图及其节点;节点指定执行专家与依赖。 |
| **DAG 编排** | 无依赖的 step 并行、有依赖的等待上游;复用 vage `orchestrate` 包。 |
| **递归深度 / 递归预算** | 经 `context.Context` 传递的委派层数计数;Dispatcher 入口统一检查上限,超限切 Fallback。 |

## 记忆与会话

| 术语 | 定义 |
|------|------|
| **三层记忆** | working(每请求,运行时管理)/ session(每对话,含事实抽取与摘要)/ persistent(跨会话,文件或 SQLite)。 |
| **Namespace(命名空间)** | 持久记忆的分类。**共享**:`project` / `user` / `conventions` / `notes` / `default`(跨会话);**会话私有**:其余(绑定 `session_id`)。 |
| **会话私有访问控制** | 代理写入当前会话私有 namespace 的条目不能被其他会话读/改/删;CLI/HTTP 走 user-path,仅限共享 namespace。 |
| **Session(会话)** | 元数据 + 事件流持久化。删除时一次目录递归删除清掉全部子系统状态。 |
| **Plan Workspace** | 跨会话持久化的 `plan.md` + `notes/`;**只有 Primary 能写**,专家只读。 |
| **Session Tree** | 长任务的结构化目标-子任务图,支持折叠(Promotion)渐进抽象。默认关闭。 |
| **上下文压缩(Compaction)** | 主动 auto-compact(逼近模型上限)+ 反应式 emergency compact(溢出错误)+ 工具输出截断。 |

## 工具与安全

| 术语 | 定义 |
|------|------|
| **ToolProfile / 能力分级** | 代理可用工具不硬编码,而声明一个 profile:Full / Review / ReadOnly / None。 |
| **read_only 属性** | 工具是否仅读取/观察而不修改状态;决定其在 plan 模式下是否可用。 |
| **工作区 allow-list** | 文件工具(os.Root)与 bash 路径参数的可访问目录白名单,默认 cwd + 系统临时目录。 |
| **Bash 风险分级** | 对每条 bash 子命令分类:Safe / Caution / Dangerous / Blocked。 |
| **工具结果注入扫描** | 间接提示注入防御:每个工具返回在进入模型前被扫描(20 条规则),动作 log / rewrite / block。 |
| **MCP 凭据过滤** | MCP I/O 边界扫描凭据与敏感字段,动作 log / redact / block。 |
| **权限模式(Permission Mode)** | CLI 下控制工具授权:default / accept-edits / auto / plan。 |

## 成本、预算与可观测

| 术语 | 定义 |
|------|------|
| **Token Usage** | 从 LLM 响应抽取的 input / output / cache-read token 数。 |
| **Cost Tracker** | 进程生命周期内的 token 与 USD 成本累计(按可配置价格表折算)。 |
| **Budget(预算)** | session / daily 维度的 token / cost 硬上限 + 软告警阈值;超限拒绝下一次 LLM 调用(HTTP→429)。 |
| **Trace** | 可选的结构化事件流 JSONL 落盘(按项目散列 + 会话 id 分目录)。 |
| **Debug** | 开发期逐次 LLM/工具 I/O 完整记录(不截断、不脱敏)。 |
| **事件总线** | vage 的统一事件分发基础;trace / session / budget / tree 等都作为旁路订阅者挂载。 |
| **零成本默认路径** | 子系统未启用 → 不构造 → 不挂事件 → 零开销。 |

## 运行模式

| 术语 | 定义 |
|------|------|
| **CLI 模式** | 默认;Bubble Tea TUI,交互工具走对话框。 |
| **`-p` 单提示** | CLI 系下跑一次即退出的非交互模式。 |
| **`-eval` 评测** | CLI 系下跑 JSONL 数据集并输出报告 + 退出码。 |
| **HTTP 模式** | REST + SSE;sync / streaming / async 三种交互。 |
| **MCP 模式** | 把代理暴露为 MCP 工具;stdio 或 Streamable HTTP 传输。 |
