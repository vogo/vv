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
  ├── events.jsonl         # session.FileSessionStore —— append-only 事件流
  ├── state.json           # session.FileSessionStore —— 状态 KV
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

## Session 子系统

负责对话历史的持久化。设计要点:

- **元数据 + 事件流分离**:元数据(`meta.json`)小而频繁更新(last accessed、title),事件流(`events.jsonl`)是追加写性质。两类负载用不同文件,避免互相影响;`updated_at` **不**反映事件追加(高频追加不刷此字段以减 I/O),使 `Get` 保持 O(1)。状态 KV(`state.json`)覆盖语义,单独寻址。
- **异步 hook 写入**:事件不在主路径上同步落盘,而是通过事件总线异步写(SessionHook 与 trace 同为旁路订阅者)。代价是关闭进程时需主动 flush——Shutdown 在解耦的独立 3s 上下文执行(CONFIG-R12);收益是在线请求延迟与子系统启用与否无关。
- **id-only 恢复**:当前 MVP 复用 id 让记忆/plan/tree 共目录,但**不重放对话历史**。完整 checkpoint+replay 在路线图中。
- 设计上**没有引入"会话状态机"**——会话只是一组按时间顺序写入的事件,任何"当前状态"都可由事件回放计算得到(SESS-R8)。`state` 字段(active/paused/completed/failed)是元数据标签,切换不影响事件追加。
- **自动创建**:SessionHook autoCreate(默认)在首个事件追加时隐式创建会话,`agent_id` 取自首个事件;CLI 的 `TouchSession` 可显式创建。

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

CLI 提供:列出会话、按 id 恢复、强制开新会话、按 id 打印 tree(可选包含已折叠节点)。

HTTP 提供完整 REST 视图:会话列表/详情/事件、Plan Workspace 文件读取、Session Tree 节点 CRUD 与折叠操作。

`DELETE /v1/sessions/{id}` 因为共根设计,单一调用清掉全部三套子系统的状态(SESS-R1)。HTTP 路由契约细节归 [http-api](../http-api/http-api-overview.md) 领域;CLI 命令归 [cli](../cli/cli-overview.md)。

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
