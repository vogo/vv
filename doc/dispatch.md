# 分发器与 Primary Assistant

## 分发器的角色

分发器对外是一个普通的 `agent.StreamAgent`，对内只做一件事：**把请求转交给 Primary，必要时切换到 Fallback Primary**。它不做意图分类、不做总结、不做策略选择——所有这些都被下放到 Primary 的工具调用里。

这种"薄分发"是从早期"intent → execute → summarize"三段管道演化而来的设计简化。原管道在每一段都需要一次额外的 LLM 调用，而 Primary 把这些合并到自己的 ReAct 循环里，由模型自行决定走哪一条路径。

## 两条物理路径

```
请求进入 → 取递归深度
             │
   深度 < 上限     深度 ≥ 上限
        │              │
        ▼              ▼
      Primary       Fallback Primary
   (完整工具集)        (无工具)
```

Fallback 路径存在的唯一目的是 **防止递归失控**：达到深度上限后，物理上消除"再次委派/再次规划"的可能，保证在有限步骤内必定回应用户。Fallback Primary 共享 Primary 的人格和系统提示，所以用户看不出切换；但它无论如何都只能直答。

## Primary 的四种选择

Primary 是一个 ReAct 循环。每一轮 LLM 给出一次响应，从下面的"动作集合"里选一个：

| 动作 | 触发的工具 | 何时使用 |
|------|-----------|---------|
| 直答 | 无 | 闲聊、定义、不依赖项目的计算 |
| 只读探查 | `read` / `glob` / `grep` / `web_fetch` / `web_search` | 需要看代码或公网资料后再回答 |
| 委派 | `delegate_to_<专家>` | 任务能干净地映射到某个专家 |
| 规划 | `plan_task` | 任务跨多个专家能力域 |

辅助动作还有：

- 进度记录：`todo_write`（同一会话内可见的检查清单）。
- 跨会话规划：`plan_update` / `notes_*`（持久化到 Plan Workspace）。
- 长任务结构化记忆：`tree_add` / `tree_update` / `tree_promote` 等（启用 Session Tree 时）。
- 一次澄清：`ask_user`（用户意图真的歧义且代价巨大时）。

Primary 的系统提示明确禁止它自己改写文件——它没有写工具，所有 mutation 必须经由 `delegate_to_coder`。

## 委派的语义

`delegate_to_<agent>` 是 Primary 的核心工具家族：每个 dispatchable 专家都有对应的一只。调用它时：

1. 递归深度 +1，传递给被委派的子代理。
2. Primary 提供任务描述与可选的"已收集到的背景"。
3. 子代理在自己的 ReAct 循环中独立完成任务，结果以工具结果形式回到 Primary。
4. Primary 把子代理的回答 **折叠** 进自己的最终回复——而不是原样转发，这样用户始终看到一个连贯的 Primary 视角。

子代理失败不会冒泡为 Run 错误，而是以 `IsError=true` 的工具结果返回。这让 Primary 能基于错误内容继续决策（例如改派另一个专家、改用直答、向用户澄清），而不是让整轮请求 abort。

## 规划的语义

`plan_task` 触发 DAG 编排：

1. Primary 给出 goal + steps；每个 step 指定执行的专家名与依赖。
2. 分发器构造 DAG：无依赖的 step 并行执行，有依赖的等待上游。
3. 多终端结果由 PlanGen 汇总成单一文本返回 Primary。
4. 整个 DAG 共享一个递归预算（在 Primary 的预算上 +1）。

DAG 节点也支持 **动态规格**——某 step 的执行者由 spec 临时构造（指定 base type + 工具子集 + 自定义系统提示），用于"为这一步定制一个稍有差异的代理"的场景。

## 流式事件

每次请求会发出一对 phase 事件包住 Primary 的整个执行：

- `EventPhaseStart{Phase:"unified_primary"}`
- `EventPhaseEnd{Phase:"unified_primary", duration, toolCalls, promptTokens, completionTokens}`

Fallback 路径上额外发一对 `summarize` 静态 phase 事件（零 LLM 调用），让 SSE 消费者不需要分支判断走哪条路径。

## 递归预算的传递

预算通过请求上下文承载，途径：

- 分发器入口处读一次。
- `delegate_to_*` 与 `plan_task` 的处理逻辑各自 +1 后传给下层。
- 下层若再次进入分发器（专家自己又触发分发器，例如通过 ask_user 链），同一个上限会再次生效，不可能突破。

## Session Tree 镜像

启用 Session Tree 且打开"分发器写树"开关时，每次 `plan_task` 都会把 plan 镜像为树节点：第一次创建 goal 根，后续追加为子树。失败仅记录告警，不阻塞 DAG 执行——树是辅助视图，不是关键路径。

## 与 vage 的边界

- 分发器实现 vage 的 `agent.Agent` / `agent.StreamAgent` 接口，所以它可以被 HTTP service 当作普通代理注册。
- DAG 执行复用 vage 的 `orchestrate` 包，分发器只提供 step 列表与节点的输入映射器。
- 事件流复用 vage 的 schema 事件类型，没有 vv 私有事件。

更深层的 DAG 执行模型见 `vage/.doc/orchestrate.md`。
