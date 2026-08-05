# session 存储精简方案（设计存档）

> 状态：**已落地**（P0 / P1 / P2 全部完成）。本文保留现状实测与推导过程作为设计存档；生效中的规范见 [design.md](design.md) 与 [spec.md](spec.md)（SESS-R10 / SESS-R11）。
> 目标是在**完全保留 vv 既有能力**的前提下，消除会话存储的重复写入，并给子代理（sub-agent）一个明确、可寻址的落盘位置。
> 参考实现：Claude Code 的 `~/.claude/projects/<project>/<sessionID>.jsonl` + `<sessionID>/subagents/agent-*.jsonl` 布局。
>
> **落地与提案的差异**（以实现为准）：
> - 外置文件是 `tool-results/<id>.json`（存整条消息 JSON），不是 `.txt`：按协议切出正文再塞回去需要 OpenAI / Anthropic 两套线格式的手术，整条外置则协议无关且读取端可透明还原。
> - 消息哈希**排除 `Timestamp`**。提案未明确这点，实测发现每轮重建的 system prompt 只有创建时间不同，把它算进哈希等于放弃跨轮去重。
> - `GET /v1/sessions/{id}/children` 是**标记弃用**而非改指向：它当前恒返回空列表，另起 `/subagents` 不破坏任何契约。
> - `event_persist: none` 保留 `agent_start` 一条：HTTP 模式下 `meta.json` 靠 SessionHook 的 autoCreate 产生，真正静默会让会话有事实源却没有元数据。

---

## 1. 现状实测

样本：`~/.vv/sessions/_Users_tiltwind_workspace_github_vogo_vv/1785849640329819000-ec0cafeb9f849f8c`（2 个 turn、7 次 ReAct iteration 的短会话）。

| 文件 | 实际字节 | 其中唯一内容 | 放大倍数 |
|------|---------|------------|---------|
| `checkpoints/*.json`（7 份） | 70,309 | 19,978（17 条唯一消息） | **3.5×** |
| `events.jsonl` | 106,834 | ~15,410（控制面事件） | **6.9×** |
| `build_reports/*.json`（2 份） | 1,430 | 0（与 `context_built` 事件字段级等价） | ∞ |
| `meta.json` / `state.json` / `metrics.json` | 556 | 556 | 1× |
| **合计** | **179,129** | **~35,900** | **≈ 5×** |

放大来源逐条定性：

### 1.1 checkpoints —— O(n²) 全量快照

`vage/agent/taskagent` 在**每次 iteration 结束**调 `IterationStore.Save`，而 `checkpoint.Checkpoint.Messages` 是**完整消息数组**。于是第 k 份 checkpoint 里包含了第 1..k-1 份的全部内容：

```
seq 1  msgs  5   4,542 B
seq 2  msgs  7   7,042 B
seq 3  msgs  8   7,901 B   ← turn 1 结束
seq 4  msgs  5   5,030 B   ← turn 2 开始
seq 5  msgs  8  12,641 B
seq 6  msgs 10  16,080 B
seq 7  msgs 11  17,073 B   ← 已包含全部内容
```

仅 2,668 字节的 system prompt 就被原样写了 7 遍（18.7 KB）。会话越长放大越猛：迭代数 n 时存储是 O(n²)，而信息量是 O(n)。

### 1.2 events.jsonl —— 75% 是流式增量

事件类型分布：

| 类型 | 条数 | 字节 | 占比 |
|------|-----|------|-----|
| `text_delta` | 496 | 80,882 | **75.7%** |
| `tool_result` | 7 | 10,542 | 9.9% |
| `checkpoint_written` | 7 | 1,893 | 1.8% |
| `context_built` | 2 | 1,276 | 1.2% |
| `agent_end` / `todo_update` / 其它 | 27 | ~12,241 | 11.4% |

`text_delta` 是 UI 流式渲染用的增量，逐 token 落盘后拼起来**恰好等于** checkpoint 里那条 assistant 消息；`tool_result` 的正文**等于** checkpoint 里那条 tool 消息；`context_built` 的 data **逐字段等于** `build_reports/*.json`。

更讽刺的是：**体量最大的这份数据恰恰是唯一无法回放的**。`schema.Event.Data` 是接口类型，从 `events.jsonl` 反序列化回来是 `nil`（`cli/cli.go:321` 已注明），所以 `--resume` 至今只能做 id-only 恢复，横幅上写着 `history not restored`。80 KB 只写不读。

### 1.3 build_reports —— 100% 冗余

`build_reports/000001.json` 与 `context_built` 事件的 `data` 字段一一对应（`builder` / `strategy` / `output_count` / `output_tokens` / `sources[]` / `duration_ms`），只是 `budget_total` 改叫 `input_budget`。同一份数据落了两次盘。

### 1.4 trace —— 第三份副本（opt-in）

`trace.enabled: true` 时 `traces/tracelog` 把**同一批事件**再写一份到 `~/.vv/traces/<projectHash>/<sessionID>.jsonl`。除了内容重复，它还引入了**第二套目录约定**：tracelog 用 `ProjectHash(workingDir)`，session 用 `SessionProjectName(workingDir)`（人类可读），同一个项目在磁盘上有两个互不关联的桶名。

### 1.5 子代理 —— 根本没有落盘（"找不到"的真因）

`dispatches/primary_tools.go:128`：

```go
req := schema.RunRequest{
    Messages: []schema.Message{schema.NewUserMessage(ag.Protocol(), input)},
}   // ← 没有 SessionID
resp, err := ag.Run(ctx, &req)
```

`taskagent` 的 `rc.sessionID = req.SessionID`，于是 delegate 出去的 coder / researcher / reviewer 全程 `sessionID == ""`，后果是：

- `IterationStore.Save` → `ErrInvalidArgument`，只打一条 warn，**checkpoint 不落盘**；
- 它发出的每个事件 `SessionID == ""` → `SessionHook.consume` 里 `if ev.SessionID == "" { continue }` 直接丢弃，**事件也不落盘**。

所以磁盘上 18 份 checkpoint 的 `agent_id` **全是 `primary`**，子代理干了什么一个字都没留下。

而另一条路径（DAG 编排，`dispatches/dag.go:117`）**传了** `SessionID: req.SessionID`——两条委派路径行为不一致，且一旦生效会带来新问题：子代理的 checkpoint 会挤进主会话**同一个 sequence 序列**，`Load(sessionID, "")`（"取 sequence 最大的那份"）可能返回 coder 的消息数组，`--resume` 会把子代理的上下文当成主对话恢复。

配套的 `GET /v1/sessions/{id}/children` 依赖 `Session.ParentID`，而全仓库**没有任何地方写入 ParentID** —— 这是个永远返回空列表的死接口。

---

## 2. 问题定性

一句话：**同一份内容有多个"所有者"，而真正需要的那份（可回放的对话）反而缺位。**

| 症状 | 根因 |
|------|------|
| checkpoints O(n²) | 快照语义 —— 每次存全量而非增量 |
| events.jsonl 75% 冗余 | 事件面无过滤，把 UI 流式增量当成审计事实持久化 |
| build_reports 全冗余 | 同一数据配了两个 sink |
| `--resume` 恢复不了历史 | 唯一的"全量流水"用了不可反序列化的接口类型 |
| 子代理找不到 | 委派时丢了 SessionID；且没有 agent 维度的存储分区 |
| trace 双写 + 双目录约定 | 两个订阅者各写各的，目录命名策略还不一样 |

---

## 3. 设计原则

**每一份内容有且仅有一个 owner 文件；其它地方只允许存指针。**

| 内容 | owner | 其它位置的形态 |
|------|-------|--------------|
| 消息正文（system / user / assistant / tool） | `messages.jsonl` | checkpoint 行存 16 字符消息 id；事件不存 |
| 流式增量 `text_delta` | **不落盘** | 内存直通 UI；最终文本已在 messages.jsonl |
| 工具输出 | `messages.jsonl` 的 tool 消息（超阈值外置到 `tool-results/`） | `tool_call_start/end` 事件只留 name/args/duration |
| 上下文构建报告 | `build_reports/NNNNNN.json` | `context_built` 事件不落盘 |
| 计量 | `metrics.json` | —— |
| 控制面时间线 | `events.jsonl` | —— |
| 全量事件流（调试用） | `events.jsonl`（`event_persist: all`） | 不再另起 `~/.vv/traces` 树 |

---

## 4. 目标布局

```
~/.vv/sessions/<project>/<sessionID>/
├── meta.json                      # 元数据（不变）
├── state.json                     # 状态 KV（不变）
├── metrics.json                   # 计量（不变）
├── messages.jsonl                 # ★ 新：主链唯一事实源（消息 + checkpoint 标记 + 子代理指针）
├── events.jsonl                   # 控制面事件（默认过滤重复载荷；支持按大小轮转）
├── build_reports/NNNNNN.json      # 保留（LRU 上限不变），事件面不再重复
├── subagents/
│   └── <agentID>-<runSeq>.jsonl   # ★ 新：子代理事实源，与主链同构
├── tool-results/<id>.json         # ★ 新：超阈值工具消息外置
├── workspace/                     # 不变（plan.md / notes / scratch / artifacts）
└── tree/tree.json                 # 不变
```

**移除 `checkpoints/` 目录**（能力迁移到 `messages.jsonl`，见 §5）。

共根删除一致性（SESS-R1 / constitution §4）不但保持，还更强：新增的 `subagents/`、`tool-results/` 都在同一根下，`os.RemoveAll(<root>/<id>)` 一次清干净。

---

## 5. messages.jsonl 规范

append-only，每行一个 JSON 对象，用 `k` 区分行类型：

```jsonc
// 消息行：内容寻址，同一内容全会话只写一次
{"k":"msg","id":"a3f1c9d2e4b60817","role":"assistant","ts":"...","message":{/* schema.Message 原样 */}}
// 外置形态：正文写入 tool-results/<id>.json，行内只留指针
{"k":"msg","id":"c07d...","role":"tool","spill":"tool-results/c07d....json","spill_bytes":41233}

// checkpoint 行：只存消息 id 序列，不存正文
{"k":"ckpt","seq":7,"agent":"primary","iter":3,"final":true,"stop":"complete",
 "usage":{"prompt_tokens":20815,"completion_tokens":1225,"cache_read_tokens":18176},
 "msgs":["6b2a...","a3f1...","c07d..."]}

// 子代理指针行：主链上标记一次委派，正文在 subagents/ 下
{"k":"subagent","agent":"coder","run":1,"file":"subagents/coder-1.jsonl","task":"...","ts":"..."}

```

### 5.1 写入算法

`Save(ctx, cp)` 时：

1. 对 `cp.Messages` 的每条消息算 `sha256(canonical_json with Timestamp zeroed)[:16]` 作为 `id`（存储时仍写入首次出现的完整消息，含原时间戳）；
2. 内存中维护该文件的 `seen map[id]struct{}`（进程冷启动时扫一遍文件重建）；未见过的写 `k:"msg"` 行；
3. 追加一行 `k:"ckpt"`，`msgs` 为本次 iteration 的**有序 id 列表**；
4. 返回时回填 `cp.Sequence` / `cp.ID` / `cp.CreatedAt` —— `IterationStore` 接口契约不变。

**为什么用内容寻址而不是"前缀增量"**：实测 turn 2 的消息数组是 8 → 5（上下文压缩/编辑会重写、丢弃、重排消息），前缀 diff 会频繁失配退化成全量。内容寻址对压缩、重排、重新引入历史消息都天然正确；被压缩掉的旧消息仍留在文件里可审计，只是不再被任何新 ckpt 行引用。

### 5.2 读取

- `Load(sessionID, "")` = 取主链最后一条 ckpt 行（子代理的 ckpt 行不在这个文件里，因此结构上不可能取到），按 `msgs` id 列表还原消息数组。**这同时修掉了"resume 可能恢复到子代理上下文"的隐患。**
- `Load(sessionID, id)` = 按 ckpt 行的 `id` 匹配。
- `List(sessionID)` = 只解析 ckpt 行 → `[]*CheckpointMeta`，`MessagesCount = len(msgs)`，不读正文，扫描仍是轻量的。
- `ErrCheckpointNotFound` / `ErrAlreadyFinal` 的返回时机与现实现完全一致，HTTP 状态码映射（404 / 409 / 503）零变更。

文件很大时（> 几 MB）可选加 `index.jsonl` 记 `{seq, byte_offset}` 做 O(1) seek —— 先不做，参考实现（Claude Code）也是整文件扫。

### 5.3 白捡的能力：真恢复

`messages.jsonl` 存的是**具体类型**（`schema.Message`），不是接口，能无损反序列化。于是 `--resume` 可以从 id-only 升级为**真正回放对话历史**，把悬空多时的 `session.history_replay_max_events` 配置项兑现（改语义为 `resume_max_messages`，旧 key 兼容解析）。

### 5.4 体积对比

样本会话按新方案重算：

| | 现状 | 新方案 |
|---|---|---|
| 消息存储 | 70,309（checkpoints） | ~20,600（19,978 唯一消息 + 7 条 ckpt 行 ≈ 600 B） |
| 事件 | 106,834 | ~15,400（剔除 text_delta / tool_result / context_built） |
| build_reports | 1,430 | 1,430 |
| 其它 | 556 | 556 |
| **合计** | **179,129** | **~38,000（≈ 4.7× 缩减）** |

关键是渐近行为：消息存储从 **O(n²) 降到 O(n)**，30 次 iteration 的长会话收益远不止 5 倍。

---

## 6. 子代理存储

### 6.1 落盘

- 每次委派 = 一次 **run**，独立文件 `subagents/<agentID>-<runSeq>.jsonl`，格式与主链**完全同构**（同样的 msg / ckpt 行）。
- `runSeq` 由 store 在 `(sessionID, agentID)` 维度单调分配；委派方（delegate handler 与 DAG 执行器）生成 runID 放进 ctx，store 从 `ctx` 取，避免靠"猜边界"。
- 主链 `messages.jsonl` 同步写一行 `k:"subagent"` 指针 —— **这就是"子代理存在哪"这个问题的答案：主 transcript 上有明确指针，顺着 `file` 字段打开即可。**

### 6.2 必须同时改的两处

1. `dispatches/primary_tools.go:128` 补上 `SessionID: schema.SessionIDFromContext(ctx)`（vage 的 `tool_batch.go:75` 已经把 sessionID 放进 tool handler 的 ctx，取用即可）；
2. store 按 `cp.AgentID` 分流：`primary` → `messages.jsonl`，其余 → `subagents/`。

**两者必须一起上线**：只做 1 会让子代理 checkpoint 挤进主 sequence 污染 resume；只做 2 则子代理依旧无 sessionID，什么都写不出来。

### 6.3 HTTP 面

- 新增 `GET /v1/sessions/{id}/subagents` → 列出 run（`agent_id` / `run_seq` / `task` / `started_at` / `iterations` / `final`），以及 `GET /v1/sessions/{id}/subagents/{agent}/{run}` 读单次 run 的消息。
- 现有 `GET /v1/sessions/{id}/children` 保留但标记 deprecated（它当前恒为空列表，因此**不构成契约破坏**），文档指向新端点。

---

## 7. 事件面精简

`setup/setup.go:1102` 现在是 `session.NewSessionHook(store)` —— 无过滤，全量落盘。vage 已提供 `session.WithFilter(types...)`，改成白名单即可，**零新代码**：

| `event_persist` | 行为 |
|---|---|
| `control`（新默认） | 白名单：agent/iteration/tool_call/phase/sub_agent/error/budget/guard/skill/todo/workspace/session_tree/checkpoint_written/context_edited 等控制面事件 |
| `all` | 不过滤（等价于今天的行为 + 取代 `trace.enabled`） |
| `none` | 不落事件，只保留 messages.jsonl |

被排除的三类及其 owner：`text_delta`（不落盘）、`tool_result`（正文在 messages.jsonl）、`context_built`（在 build_reports/）。

**trace 合并**：`trace.enabled: true` 且 session 开启时，等价于 `event_persist: all`，不再写 `~/.vv/traces/<projectHash>/`。tracelog 的按大小轮转（默认 64 MiB）能力迁移到 `events.jsonl`。这样磁盘上只剩**一套**目录约定，`ProjectHash` 与 `SessionProjectName` 的双轨问题一并消失。session 关闭时 tracelog 保持原样（它是 session 关闭场景下唯一的事件落盘途径）。

---

## 8. 能力保全对照

| 既有能力 | 是否保留 | 说明 |
|---------|---------|------|
| `vv --resume` / `POST /v1/sessions/{id}/resume` | ✅ 增强 | 接口与状态码不变；从 id-only 升级为真回放；不再误恢复到子代理 |
| `GET /v1/sessions/{id}/events` | ✅ | 默认少了 `text_delta`；`event_persist: all` 可完全还原 |
| `GET /v1/sessions/{id}/build-reports` | ✅ | sink 与响应体不变 |
| `GET /v1/sessions/{id}/metrics` | ✅ | 不变 |
| Plan Workspace（plan / notes / scratch / artifacts） | ✅ | 完全不变 |
| Session Tree（含折叠、向量索引） | ✅ | 完全不变 |
| `DELETE /v1/sessions/{id}` 共根一次清 | ✅ 增强 | 新增子目录同在根下 |
| CLI 会话列表 / 恢复 / 打印 tree | ✅ | 不变 |
| trace JSONL | ✅ 合并 | 由 `event_persist: all` 承载，落在会话目录内 |
| `GET /v1/sessions/{id}/children` | ⚠️ 弃用 | 当前恒空；由 `/subagents` 取代 |
| 子代理可观测 | 🆕 | 从"完全不落盘"变成可寻址 |

不变量：SESS-R1（共根删除）加强，SESS-R2（Workspace 写者唯一）不受影响，SESS-R3（启用强校验）不变，SESS-R8（会话即事件序列，无状态机）不变。

---

## 9. 配置变更

```yaml
session:
  enabled: true
  dir: ~/.vv/sessions

  event_persist: control          # 新：control(默认) | all | none
  events_max_file_bytes: 67108864 # 新：从 trace 迁来的轮转阈值，0=不轮转
  tool_result_max_inline_bytes: 8192  # 新：超过则外置到 tool-results/，消息内留指针
  resume_max_messages: 5000       # 语义化改名（原 history_replay_max_events，旧 key 兼容）
  retention_days: 0               # 新：0=不清理；>0 时启动清理超期会话目录

  persist_build_reports: true     # 不变
  build_report_limit: 50          # 不变
```

弃用键继续解析并打 warn，一个小版本后移除。

---

## 10. 落地阶段

### P0 —— 止血（纯 vv 侧，无格式变更）✅

| # | 触点 | 改动 |
|---|------|------|
| 1 | `setup/setup.go:1102` | `NewSessionHook(store, session.WithFilter(controlPlane...))`，新增 `setup/eventfilter.go` |
| 2 | `dispatches/primary_tools.go:128` | 补 `SessionID: schema.SessionIDFromContext(ctx)` |
| 3 | `setup/checkpoint.go` | store 按 `cp.AgentID` 分流；`Load(sid,"")` 只认 primary |
| 4 | `configs/config.go` | 新增 `event_persist`，默认 `control` |

**收益**：events.jsonl 立降 ~85%，子代理开始落盘，resume 污染隐患消除。

### P1 —— messages.jsonl（核心）✅

| # | 触点 | 改动 |
|---|------|------|
| 1 | 新增 `setup/sessionlog/`（或 `memories/` 同级新包） | 实现 `checkpoint.IterationStore`：内容寻址 append + ckpt 行 + 子代理分流 |
| 2 | `setup/checkpoint.go:53` | `checkpoint.NewFileIterationStore` → 新实现（单点替换） |
| 3 | `cli/resume.go` / `httpapis/resume.go` | 恢复时回放历史消息，横幅去掉 `history not restored` |
| 4 | `httpapis/` | 新增 `/subagents` 端点 |

**收益**：消息存储 O(n²)→O(n)，`checkpoints/` 目录消失，`--resume` 真恢复。

### P2 —— 收尾 ✅

工具结果外置 `tool-results/`（`sessionlogs`）；trace 合并进 `event_persist: all`（`setup/setup.go`）；`events.jsonl` 轮转（`setup/eventrotate.go`，读侧按序拼接）；`retention_days` 清理（`setup/retention.go`）；文档回写 `design.md` / `spec.md` / `models.md` / `session-overview.md` 以及 configuration、trace、http-api 三个领域的相关段落。

---

## 11. 迁移与兼容

- **读旧写新**：新 store 的 `Load` / `List` 在 `messages.jsonl` 不存在时**回退到旧 `checkpoints/` 目录**，老会话照常 resume；新写入只走新格式。
- 旧 `checkpoints/` 不再增长；`retention_days` 或手工清理带走它。
- 不做数据迁移脚本 —— 会话是短生命周期数据，双读一个小版本后直接下线回退路径。
- vage 侧**零改动**：`checkpoint.IterationStore` 与 `session.WithFilter` 都是既有接口，无需等待 vage 发版。

---

## 12. 取舍与风险

| 决策 | 收益 | 代价 / 风险 |
|------|------|-----------|
| 内容寻址去重 | 对压缩、重排、重引入历史都正确；存储 O(n) | 需内存持有 `seen` 集合（每会话数百条，量级可忽略）；冷启动扫一次文件 |
| checkpoint 退化为 id 序列 | 单份 ckpt 行从 17 KB 降到 ~250 B | 单条消息损坏会波及引用它的所有 ckpt（append-only + 原子写可控） |
| 默认丢弃 `text_delta` | 事件面降 76% | 逐 token 时序不可事后审计 —— 需要时开 `event_persist: all` |
| 子代理一 run 一文件 | 可寻址、可单独恢复、不污染主 sequence | 高频委派的会话文件数增多（`subagents/` 下扁平，`retention_days` 兜底） |
| trace 并入 events | 一套目录约定，消除第三份副本 | `trace.dir` 自定义路径的用户需迁移（弃用期 warn） |
| 保留 `build_reports/` 而非并入事件 | REST 契约零变更；它是唯一能反序列化读回的那份 | 多一个小目录（受 LRU 上限约束） |

### 一个值得单独记录的观察

`schema.Event.Data` 是接口类型，导致 `events.jsonl` **只能写不能读**。本方案绕开它（另立具体类型的 `messages.jsonl`）而非修它。若将来 vage 给 `Event` 补上按 `type` 分派的 `UnmarshalJSON`，事件流就能同时充当审计与回放载体，届时可以再评估是否进一步合并 —— 但**不应**成为本次精简的前置依赖。
