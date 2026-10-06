# session — Domain Design

底层文件存储协议属 vage,本文档引用而不复述。

## 三子系统共根目录

vv 把三个 vage 子系统组合到同一个会话目录下:

- **Session** —— 元数据 + 事件流持久化。
- **Plan Workspace** —— 任务级"计划文件 + 笔记目录"。
- **Session Tree** —— 长任务的结构化目标-子任务图。

它们共用一个目录根,`Session` 删除时**一次目录递归删除**即可清掉所有三个子系统的状态——这是设计上的关键决策,避免跨子系统的协调一致性问题(SESS-R1,constitution § 4)。

```
<session-root>/<project>/<sessionID>/
  ├── meta.json            # session.FileSessionStore —— 元数据
  ├── events.jsonl         # session.FileSessionStore —— append-only 事件流(超限轮转为 events.N.jsonl)
  ├── state.json           # session.FileSessionStore —— 状态 KV
  ├── metrics.json         # session.MetricsStore —— 计量
  ├── messages.jsonl       # sessionlogs.Store —— 主链对话事实源(内容寻址)
  ├── subagents/
  │   └── <agent>-<n>.jsonl  # sessionlogs.Store —— 每次委派一份同构事实源
  ├── tool-results/<id>.json # sessionlogs.Store —— 超阈值工具消息外置
  ├── build_reports/NNNNNN.json  # vctx.FileBuildReportSink —— 每轮上下文构建报告
  ├── workspace/           # workspace.FileWorkspace
  │   ├── plan.md
  │   └── notes/<name>.md
  └── tree/
      └── tree.json        # tree.FileTreeStore
```

- `<session-root>` 默认 `~/.vv/sessions`,经 `session.dir` / `VV_SESSION_DIR` 覆盖。
- `<project>`(project_path_name)由 `setup.SessionProjectName(BashWorkingDir)` 从工作目录派生:路径分隔符→`_`、ASCII 字母数字原样、其他→`-`、空目录→`default`。设计目的是**人类可读**(运维可直接 `ls` 找回项目对应会话),代价是仅在标点上有差异的不同项目路径可能落到同一桶——本地单机文件存储足以承担,跨机分布式请改用 ProjectHash。
- 权限:目录 `0o700`、文件 `0o600`。
- 一次 `os.RemoveAll(<root>/<id>)` 清理所有 session 资源,无需额外协调。

## 启用关系

依赖关系在装配阶段被强制校验:开 Session Tree 但关 Session 直接启动报错,而不是沉默忽略,避免用户配置错误下的隐性问题(SESS-R3、CONFIG-R3)。

| 子系统 | 默认 | 启用条件 |
|--------|------|---------|
| Session | 开 | 默认开;显式关 → 三者全关 |
| Plan Workspace | 跟随 Session | 不可单独控制(共用会话根) |
| Session Tree | 关 | 显式开 + Session 必须开 |

`session.enabled=false` 时:workspace 不构造、工具不注册、Source 不挂载、HTTP 路由不挂载——零开销(constitution § 6)。

## 单一所有者原则

会话目录里的每一份内容**有且仅有一个 owner 文件,其它位置只允许存指针**。这条规则是整个存储布局的推导起点,来源是一次实测:重设计前一个 7 次迭代的短会话占 179 KB 磁盘,而其中唯一内容只有约 36 KB —— checkpoint 每轮存全量消息(O(n²)),`text_delta` 逐 token 落盘后又等于同一段助手文本,`build_reports/` 与 `context_built` 事件逐字段等价。

| 内容 | owner | 其它位置的形态 |
|------|-------|--------------|
| 消息正文(system / user / assistant / tool) | `messages.jsonl` | ckpt 行存 16 字符消息 id;事件面不落 |
| 流式增量 `text_delta` | **不落盘** | 内存直通 UI;最终文本已在 `messages.jsonl` |
| 工具输出 | `messages.jsonl` 的 tool 消息(超阈值外置到 `tool-results/`) | `tool_call_start/end` 事件只留 name/args/duration |
| 上下文构建报告 | `build_reports/NNNNNN.json` | `context_built` 事件不落盘 |
| 计量 | `metrics.json` | —— |
| 控制面时间线 | `events.jsonl` | —— |
| 全量事件流(调试) | `events.jsonl`(`event_persist: all`) | 不再另起 `~/.vv/traces` 树 |

`build_reports/` 之所以是 owner 而非事件面,是因为它是这份数据**唯一能反序列化读回**的形态:`schema.Event.Data` 是接口,从 JSONL 读回来恒为 `nil`。

## Session 子系统

负责对话历史的持久化。设计要点:

- **元数据 + 事件流分离**:元数据(`meta.json`)小而频繁更新(last accessed、title),事件流(`events.jsonl`)是追加写性质。两类负载用不同文件,避免互相影响;`updated_at` **不**反映事件追加(高频追加不刷此字段以减 I/O),使 `Get` 保持 O(1)。状态 KV(`state.json`)覆盖语义,单独寻址。
- **异步 hook 写入**:事件不在主路径上同步落盘,而是通过事件总线异步写。代价是关闭进程时需主动 flush——Shutdown 在解耦的独立 3s 上下文执行(CONFIG-R12);收益是在线请求延迟与子系统启用与否无关。
- **事件面三档**(`session.event_persist`):
  - `control`(默认)—— 白名单订阅控制面事件;排除 `text_delta` / `tool_result` / `context_built` 三类**已有 owner 文件**的载荷。白名单而非黑名单:上游新增事件类型默认不落盘,悄悄变大的日志比缺一行更难处理。
  - `all` —— 不过滤,等价于旧行为,并**取代 trace 子系统**。
  - `none` —— 仅保留 `agent_start`。不是真的一条不留:HTTP 模式下 `meta.json` 靠 SessionHook 的 autoCreate 产生,完全静默的 hook 会让会话有事实源却没有元数据记录。
- **trace 归并**:`trace.enabled=true` 且 session 开启时**不构造 trace hook**,改为把会话自身的事件面放宽到 `all`。旧实现把同一批事件在 `~/.vv/traces/<ProjectHash>/` 下再写一份,既是重复字节,又引入了第二套目录命名(hash 桶 vs 可读项目名)。session 关闭时 trace 仍走原路径——那时它是唯一的事件落盘途径。
- **事件轮转**:`events.jsonl` 超过 `events_max_file_bytes`(默认 64 MiB,继承自 trace hook)时改名为 `events.N.jsonl`。读侧按序拼接,轮转对 API 调用方不可见。
- **真恢复**:`--resume` 从 `messages.jsonl` 回放对话历史(上限 `resume_max_messages`),不再是 id-only。事件流做不到这件事——它的 `Data` 反序列化为 `nil`。
- 设计上**没有引入"会话状态机"**——会话只是一组按时间顺序写入的事件,任何"当前状态"都可由事件回放计算得到(SESS-R8)。`state` 字段(active/paused/completed/failed)是元数据标签,切换不影响事件追加。
- **自动创建**:SessionHook autoCreate(默认)在首个事件追加时隐式创建会话,`agent_id` 取自首个事件;CLI 的 `TouchSession` 可显式创建。
- **保留策略**:`session.retention_days > 0` 时启动扫一次,删除超期会话目录。判龄取会话内活动文件(`messages.jsonl` / `events.jsonl` / `meta.json` / `state.json` / `metrics.json`)mtime 的最大值——**不能用目录 mtime**,因为向已存在的文件追加不会更新它,天天在用的会话看起来会像从未动过。无法判龄的目录一律保留:删用户历史是不可逆操作,任何歧义都往"保留"倒。

## 对话事实源 —— messages.jsonl

`sessionlogs.Store` 实现 vage 的 `checkpoint.IterationStore`:TaskAgent 照旧在每轮迭代末尾递交**完整消息数组**,去重发生在存储层,而不是要求 agent 循环自己算增量。

每行一条记录,`k` 区分类型:

| `k` | 含义 |
|-----|------|
| `msg` | 一条消息正文。同一内容在一个文件里**至多写一次** |
| `ckpt` | 一次迭代快照,只存**有序的消息 id 列表**,不含正文 |
| `subagent` | 委派指针,指向 `subagents/<agent>-<n>.jsonl` |

- **内容寻址**:id = 消息 JSON(Timestamp 置零后)的 SHA-256 前 8 字节。选内容寻址而非"前缀增量",是因为实测上下文压缩会重写、丢弃、重排消息(样本里一轮 8 条压到 5 条),前缀 diff 会频繁失配退化成全量;内容寻址对压缩、重排、历史消息重新入列都天然正确。被压缩掉的旧消息仍留在文件里可审计,只是不再被任何新 ckpt 行引用。
- **Timestamp 不参与哈希**:每轮重建的 system prompt 除创建时间外逐字节相同,把时间戳算进哈希就等于放弃去重(样本里是每次迭代重写 2.6 KiB)。代价是重复出现的同内容消息恢复时带首次出现的时间戳——对相同内容而言这个时间戳本就没有语义。
- **Sequence 按文件单调**:主链的序列即"会话级",与旧实现一致;子代理的一次委派从 1 开始自己编号,这正是让一次委派可以脱离上下文独立阅读的原因。
- **Load(id="") 只认主链**:旧布局下主/子代理 checkpoint 挤在同一序列里,"取序列最大的那份"可能返回专家代理的消息数组,`--resume` 会把子代理上下文当成主对话恢复。现在结构上不可能发生。
- **skill_evolution 只读本事实源**:进化读主链 latest checkpoint 的完整 Messages(累计历史)与子代理 run,不另写 transcript 格式。资格门的 ToolCalls 只扫 latest `Load`,Turns 取 `Store.List` 条数。
- **旧格式回退**:`messages.jsonl` 不存在时 `Load` / `List` 落到旧 `checkpoints/` 目录,老会话照常恢复;新写入只走新格式,一个小版本后下线回退路径。

## 子代理存储

一次**委派 = 一次 run**,独立文件 `subagents/<agentID>-<runSeq>.jsonl`,格式与主链完全同构。主链同步写一行 `k:"subagent"` 指针 —— 这就是"子代理的工作存在哪"的答案。

重设计前它根本没落盘:`delegate_to_<agent>` 工具构造 `RunRequest` 时漏了 `SessionID`,于是专家代理全程 `sessionID == ""`,checkpoint 保存返回 `ErrInvalidArgument` 只打一条 warn,发出的事件又被 `SessionHook` 按空 id 丢弃。修复必须两件事一起做:

1. 委派时从 ctx 取回 session id(vage 的 `taskagent/tool_batch` 已把它放进工具处理器的 ctx);
2. 存储层按 `AgentID` 与 ctx 里的 `sessionlogs.Run` 分流。

只做 1 会让子代理 checkpoint 挤进主序列污染 resume;只做 2 则子代理依旧无 session id,什么都写不出来。

路由是双保险:ctx 里的 `Run` 是精确信号,同时任何 `AgentID != primary` 的 checkpoint 也一律不进主链。某条委派路径忘了打标记时,退化成"一个 agent 一个文件",而不是破坏会话的恢复时间线。

## Plan Workspace —— 协作语义层

是 vv 引入的一个**协作语义层**:

```
Primary           ←→  plan.md（任务策略，跨会话持久化）
                  ←→  notes/<name>.md（任务笔记）
专家代理           只读（WorkspaceSource 注入 prompt）

todo_write          ←→  本 turn 的检查清单（内存级）
```

设计取舍:

- **写者唯一**:只有 Primary 持有 `plan_update`/`notes_write`/`notes_read` 工具能写。专家(coder/researcher/reviewer)通过 `WorkspaceSource` 只读,需要 note 全文时由 Primary 调 `notes_read` 后回写到专家会话上下文。这是"写者唯一"模式,避免多专家并发覆盖(SESS-R2)。
- **plan vs todo**:两者并存而非替代——plan 是长策略(跨会话),todo 是短进度(仅当前 turn,内存级)(SESS-R4)。
- **容量上限**:plan.md 与每条 note 都有大小限制,超限时 LLM 看到明确错误,由模型决定如何分拆,避免无限增长导致提示词爆炸(SESS-R9)。

| 约束 | 数值 | 设计意图 |
|---|---|---|
| MaxPlanBytes | 64 KiB | 防止 plan.md 沦为日志 |
| MaxNoteBytes | 32 KiB | 单条事实卡片上限 |
| MaxNoteCount | 200 | 注入 prompt 的索引规模可控 |
| NoteNameMaxLen | 64 | 名称要短,便于索引扫读 |

- **懒创建**:首次 `plan_update`/`notes_write` 触发;Session 的 Create 不预创建 workspace 目录(避免空目录污染)。
- **截断注入**:plan.md 超过 MaxPlanBytes 时,WorkspaceSource 注入 prompt 时**保留尾部**并加省略标记——LLM 通常在底部追加新步骤,最近进度对下一步决策最重要。
- 写入并发:进程内 per-session `sync.Mutex` 串行化;跨进程未承诺。

## Session Tree —— 长任务结构化记忆

为长任务结构化记忆而设:当任务跨多轮、多分支、多并行子任务时,线性对话历史不再适合做记忆载体。Tree 提供:

- **目标-子任务的层级结构**:root → subtask → fact/observation/artifact_ref;模型可遍历、聚焦(cursor)、折叠某子树。`title` 永驻 prompt(结构信号),`summary` 按预算驻留(浓缩信号),`content_ref` 指向 workspace artifact(细节信号)。
- **折叠语义(Promotion)**:当某父节点下子任务过多或全部完成,把它折叠为一段摘要写入父节点,子节点从主视图消失。用 Tree 的层级形态实现"渐进抽象"——近处看细节,远处看摘要(SESS-R5)。

### 折叠器三档

| Promoter | 成本 | 行为 |
|----------|------|------|
| compressor(默认) | 零额外 LLM | 复用滑动窗口摘要器生成父节点 summary |
| llm | 付费 | 质量高,调 LLM 生成 summary |
| noop | 零 | 仅翻折叠位,不改 summary |

### 触发器 Any-of

折叠触发器是"Any-of"组合:`AnyOf(ChildrenCount, SubtreeBytes [, AllChildrenDone])`——子节点数过阈值、子树字节数过阈值、或全部子节点完成。具体阈值由配置决定:

| 配置 | 默认 |
|------|------|
| `promoter` | compressor |
| `children_threshold` | 8 |
| `subtree_bytes_threshold` | 8192 |
| `all_children_done` | true |

AddNode/UpdateNode 后同步判断、异步执行;per-(session, parent) singleflight 防重入。折叠有损但可逆:子节点保留并 `Promoted=true`,renderer 默认隐藏并加 `(folded: N children, M done)`;`tree_zoom_in` 工具或 `?include_promoted=1` HTTP 参数可看到。`Pinned=true` 子节点永不折叠;reshape 走"新建 + 旧节点 status=superseded"。

## Auto-enable 门控

Session Tree 启用后默认每轮请求都会渲染 tree 到 prompt 顶部。但短对话用不到树:渲染就是浪费。所以提供一个**门控阈值**(SESS-R6):

- 累积到 N 个 agent 完成(AgentEnd)事件之前,tree 视图不渲染(仍可手动激活)。
- 到达阈值后开始渲染,让真正"长对话"才付出渲染成本。

阈值是**进程级计数,重启清零**——用简单计数代替持久化复杂度。实现上 `sessionEventCounter`(`vv/setup/tree_counter.go`)以 `sync.Map[sessionID]*atomic.Int64` 计 AgentEnd,作为 SessionTreeSource 的渲染谓词;它实现同步 `hook.Hook`(每事件单次 Add,比异步 channel 更省)。这是 UX 提示而非审计事实,故权威状态归 SessionStore。

## 写树镜像

启用 Session Tree 且打开"分发器写树"开关时,每次 `plan_task` 都会把 plan 镜像为 tree 节点(SESS-R7):

- 第一次 plan 创建 goal 根节点。
- 后续 plan 在根下追加子树。
- 失败仅记录告警,不阻塞 DAG。

这让用户在 tree 视图里看到"vv 自己规划过哪些任务",把模型的内部决策外显为可观测结构。镜像源(`plan_task`)归 orchestration 领域;本领域只提供被写入的 TreeStore。

## CLI 与 HTTP 入口

CLI 提供:列出会话、按 id 恢复(含对话历史回放)、强制开新会话、按 id 打印 tree(可选包含已折叠节点)。

HTTP 提供完整 REST 视图:会话列表/详情/事件、子代理委派列表与单次委派详情、Plan Workspace 文件读取、Session Tree 节点 CRUD 与折叠操作。

- `GET /v1/sessions/{id}/subagents` —— 列出本会话的每一次委派(agent / run / task / 迭代数 / 终止状态)。
- `GET /v1/sessions/{id}/subagents/{agent}/{run}` —— 单次委派的最终消息与用量。
- `GET /v1/sessions/{id}/children` —— **已弃用**。它按 `Session.ParentID` 过滤,而 vv 从未写入该字段,因此恒返回空列表;保留一个版本,由 `/subagents` 取代。

`DELETE /v1/sessions/{id}` 因为共根设计,单一调用清掉 Session / Workspace / Tree 状态(SESS-R1)——新增的 `subagents/`、`tool-results/` 同在根下。interrupt 记录在兄弟目录 `interrupts/`,删除时额外 List+Delete,避免孤儿。HTTP 路由契约细节归 [http-api](../http-api/http-api-overview.md) 领域;CLI 命令归 [cli](../cli/cli-overview.md)。

## 技术取舍回顾

会话子系统是 vv 中"一致性收益最大"的设计区域:

| 决策 | 收益 | 代价 |
|------|------|------|
| 共根目录(ADR 0004) | 删除一致性自动达成,无跨子系统协调 | 仅标点不同的项目路径可能同桶(单机可接受) |
| 写者唯一 | 多代理并发不冲突 | 专家拿 note 全文需 Primary 中转 |
| 启用关系强校验 | 配置错误启动期暴露 | 无 |
| 默认渐进开启(auto-enable) | 短对话零负担,长对话才挂载 | 阈值进程级,重启清零(可接受,非审计事实) |
| 元数据/事件流分离 + 异步 hook | 在线延迟与子系统无关 | 需 Shutdown 主动 flush(独立 3s 上下文) |
| 折叠默认用 compressor | 零额外 LLM 成本 | 摘要质量不及 llm 档 |
| 单一 owner + 指针(见上) | 样本会话 179 KB → ~38 KB;消息存储 O(n²) → O(n) | 规则要靠约定维持,新增 sink 时必须先问"谁是 owner" |
| 内容寻址去重 | 对压缩、重排、历史重入都正确 | 每会话内存持一份 id 集合(数百条,可忽略);冷启动扫一次文件 |
| 哈希忽略 Timestamp | 跨轮 system prompt 只存一份 | 同内容消息恢复时带首次出现的时间戳 |
| 默认丢弃 `text_delta` | 事件面降约 76% | 逐 token 时序不可事后审计——需要时开 `event_persist: all` |
| 子代理一次委派一个文件 | 可寻址、可独立阅读、不污染主序列 | 高频委派的会话文件数增多(`retention_days` 兜底) |
| trace 并入事件面 | 一套目录约定,消除第三份副本 | 自定义 `trace.dir` 的用户需迁移(弃用期告警) |

设计存档(现状实测、方案推导、分阶段落地)见 [storage-redesign.md](storage-redesign.md)。
