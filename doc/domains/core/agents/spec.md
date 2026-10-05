# agents — Domain Spec

## Overview

agents 领域定义 vv 的**能力维度与预制组合**:被前门(Primary)派生去完成具体子任务的 ReAct 循环代理,由若干**正交维度**组合而成,而不是由角色名隐式决定全部行为。本领域的核心命题是 **正交能力模型**——代理可用的工具不写死在调用点,而由 ToolProfile 决定;产出形态由 Skill 决定;任务上下文由 ContextSource 决定。同一个 Factory 配上不同维度组合即产出不同能力面的执行者。

coder / researcher / reviewer 是三个**具名预制组合**(descriptor + 默认 profile + 默认提示),它们仍然可委派、仍是启动期注册的类型,但**不再是扩展能力的唯一手段**:新任务形态(如 code-review)只需声明维度组合,由 orchestration 的 worker 派生机制装配。

**范围**:能力维度(ToolProfile / Skill / ContextSource)的模型与注册表、预制组合的职责边界与工具能力面、ToolProfile 五档分级、代理描述符(AgentDescriptor)与注册表所建模的不变量。

**边界**:本领域**不含** Primary / Dispatcher 编排、递归阀门、DAG 规划与执行(归 [orchestration](../orchestration/))。它只提供编排所消费的专家代理与 profile 模型。具体工具实体与护栏归 [tools](../tools/);代理的实例化时机与依赖来自 [configuration](../configuration/) 的装配中心。

术语见 [../../../glossary.md](../../../glossary.md)。

## Core entities

| 实体 | 职责 | 详见 |
|------|------|------|
| AgentDescriptor | 单一代理类型的声明式元数据:id / 显示名 / 描述 / ToolProfile / 系统提示 / 工厂函数 / 是否 dispatchable。注册表的元素。 | [models.md](models.md) |
| AgentType | 代理底层实现类型枚举:`task`(ReAct 循环,带可选工具)/ `orchestrator`(任务理解与分发,归 orchestration) | [models.md](models.md) |
| ToolProfile | 命名的能力集合(Capabilities ⊆ {Read, Write, Execute, Search});五档预设 Full / Review / Edit / ReadOnly / None | [models.md](models.md)、[design.md](design.md) |
| Skill | 命名的**专项指令片段**(id / 描述 / instructions),追加到 worker 系统提示;**不授予任何工具、不绕过权限**。内置 `review` / `research`。 | [models.md](models.md) |
| ContextSource | 命名的**只读上下文来源**(id / 描述 / provider),渲染为 `## Context: <id> (read-only)` 块注入 worker 输入。内置 `diff`(工作区 vs HEAD)。 | [models.md](models.md) |
| 预制组合(coder / researcher / reviewer) | 三个具名维度组合,全部 dispatchable 的 task 代理 | 本文「预制组合表」、[models.md](models.md) |

> **实现说明(以代码为准)**:实际注册的 dispatchable 专家**只有三类**——coder / researcher / reviewer(见 `vv/setup/setup.go` 的 `RegisterCoder/Researcher/Reviewer`)。早期的 `chat`(无工具纯对话)与 `explorer`(只读探查)代理已**彻底移除**:闲聊由统一 Primary **无工具内联直答**承担,探查由 Primary 用自身的 read/glob/grep 工具完成,故不再有 `delegate_to_chat` / `delegate_to_explorer`(`setup.go` 委派工具家族刻意只含 coder/researcher/reviewer)。此外注册表还含一个 `planner` 代理(ProfileNone,**非 dispatchable**,仅用于内部分类),以及 Primary / Fallback Primary(非 dispatchable)。ToolProfile=None 这一能力档仍存在,由 planner 与 Fallback Primary 例示,而非 chat。

完整属性表见 [models.md](models.md)。

## 预制组合表

| 组合 | 职责 | ToolProfile | 能力面 | Dispatchable | 委派工具 |
|------|------|-------------|--------|--------------|----------|
| **Coder** | 编码:读 + 写 + 执行 + 搜索,唯一默认持有写工具的预制组合 | Full | read · write · execute · search | 是 | `delegate_to_coder` |
| **Researcher** | 研究:只读探查、读文档、抓公网资料,绝不动文件系统 | ReadOnly | read · search | 是 | `delegate_to_researcher` |
| **Reviewer** | 评审:读 + 搜索 + 跑测试/lint(bash),但不能写 | Review | read · search · execute | 是 | `delegate_to_reviewer` |

> 纯对话与只读探查不再是独立代理:由统一 Primary 内联承担(见上「实现说明」)。`planner`(ProfileNone)是内部分类代理,非 dispatchable,不出现在委派工具家族中(AGENTS-R6)。

**这三行是预制,不是穷举**。任何其它组合无需新增类型,直接声明维度即可。典型示例:

| 目标形态 | runtime | ToolProfile | Skills | ContextSources | 结果能力面 |
|---------|---------|-------------|--------|----------------|-----------|
| code-review | coder(与普通编码同一 runtime) | Review | `review` | `diff` | read · search · execute,**无 write/edit**;diff 以只读块进入输入 |

分工背后的"能力鸿沟"设计取舍见 [design.md](design.md)「能力维度与预制组合」。各预制组合系统提示词全文承载于代码(`vv/agents/{coder,researcher,reviewer}.go`),skill 指令承载于 `vv/registries/skill.go`,此处不复述。

## Business rules(不变量)

| ID | 规则 | 说明 |
|----|------|------|
| AGENTS-R1 | ToolProfile 决定工具集 | 代理可用工具**不在调用点硬编码**,完全由 ToolProfile 在装配阶段翻译而来(Capability → 具体工具):静态代理取描述符声明的 profile,派生 worker 取 spec 的 `tool_access`(省略则继承 base descriptor)。同一 Factory + 不同 profile 产出不同能力面的执行者。对应候选 ADR-0003。 |
| AGENTS-R11 | Skill 不授权 | Skill 只向系统提示追加指令,**永远不授予工具、不放宽权限**。带 skill 与不带 skill 的同一 spec 工具面必须完全相同;自定义 system_prompt 同理。 |
| AGENTS-R12 | ContextSource 只读且 allow-list | 上下文只能来自已注册的 ContextSource;provider 是纯读取器,渲染为显式标注的只读块。未注册来源在构造期报错,provider 失败则中止派生——**绝不**让 worker 在"以为读到了上下文"的状态下运行。 |
| AGENTS-R2 | Researcher 无写无 bash | researcher 预制组合 = ProfileReadOnly(read + search),**绝无** write / edit / bash。它读到的代码不能改,发现的问题只能反馈。 |
| AGENTS-R3 | Reviewer = Review 能力档 | reviewer 预制组合 = ProfileReview(read + search + execute),可跑测试/lint,但**无写工具**;它的输出是"建议下一步",由 Primary 决定是否交给具备写能力的执行者。 |
| AGENTS-R4 | 写权限只来自带 Write 能力的档 | write / edit **只**由含 Write 能力的档授予,即 ProfileFull 与 ProfileEdit。在 dispatchable 预制组合中仅 coder 默认持有 Full;派生 worker 的写能力同样只能来自显式声明的 `tool_access: full` 或 `edit`,与它选哪个 base runtime 无关——runtime 决定行为风格,profile 决定权限。Primary 在所有 execution model 下持有 Full(见 orchestration ORCH-R2)。 |
| AGENTS-R5 | ProfileNone 代理无工具 | ProfileNone 代理(`planner`、Fallback Primary)LLM-only,不挂任何工具(含 `ask_user` / `todo_write`)。闲聊由 Primary 同样以无工具方式内联直答。 |
| AGENTS-R6 | 每个 dispatchable 对应一个委派工具 | 注册表中每个 `Dispatchable=true` 的描述符,在 Primary 工具集里自动获得一个 `delegate_to_<id>` 工具,并被 PlannerAgentList 汇入"可委派目标列表"。非 dispatchable 代理(如 planner)永不出现在委派工具家族、HTTP 子端点或 MCP 工具中。 |
| AGENTS-R7 | 描述符声明一次、多处消费 | 一个新代理只需写一个 AgentDescriptor + 一个 Factory,即被工厂装配、Primary 提示拼接、委派工具家族、HTTP 子路由、MCP 暴露五条路径自动看见(具体下游见 design.md)。 |
| AGENTS-R8 | 工具能力代理只读 plan/tree | 专家代理只读 Plan Workspace / Session Tree 视图;写工具只挂给 Primary。避免多写者在同一份 plan.md 上互相覆盖(写者唯一,详见 [session](../session/) 与 [orchestration](../orchestration/))。 |
| AGENTS-R9 | ID 唯一 + 启动期校验 | 注册表 ID 冲突在启动期 panic,不允许运行期出现"半就绪"的代理表。 |
| AGENTS-R10 | 持久记忆 prompt 仅 Coder | 持久化记忆(PersistentMemory)**全量渲染进系统提示**只发生在 coder;其余专家不读持久记忆。Primary / Full 档执行者通过 `memory_set` / `memory_recall` 按需读写(MEM-R9 / CapRemember),与本规则正交。 |

> 注:能力 → 具体工具的映射表(Read 含公网抓取等)、ToolProfile 五档定义为可从代码恢复的细节,见 [design.md](design.md)「能力 → 工具映射」与 [tools](../tools/) 领域,此处不复述。

## States & transitions

专家代理是**无状态组件**,无生命周期状态机(单次 Run 内的 ReAct 迭代由 vage TaskAgent 管理;执行态由 orchestration 的 Task Plan 跟踪)。注册表本身在装配阶段被填充一次,随后转为只读视图供下游消费——见 [design.md](design.md)「与启动期一次性构造的关系」。

## Domain events

本领域不发布自有业务事件。代理运行期发出的迭代/工具/上下文事件(EventContextBuilt 等)由 vage TaskAgent 经注入的 HookManager 分发,事件归属 [trace](../trace/) 与可观测领域;本领域只负责把 HookManager 经 Factory 注入到代理(见 design.md「HookManager 注入」)。

## Interactions

| 协作领域 | 关系 |
|----------|------|
| [orchestration](../orchestration/) | 消费方:Primary 经 `delegate_to_<id>` 委派 dispatchable 专家;Dispatcher 持有子代理 map;Primary 复用本领域 ToolProfile 模型(所有执行模型下均为 Full)。 |
| [configuration](../configuration/) | 上游:装配中心创建注册表、注册描述符、按 ToolProfile 构造工具集、提供 Factory 全部依赖(LLM/记忆/护栏/Guard/Hook/IterationStore)。 |
| [tools](../tools/) | 上游:Capability 翻译为具体工具;`ask_user` / `todo_write` 由装配阶段注入工具集;注入 Guard(ToolResultGuards)挂到代理。 |
| [http-api](../http-api/) | 下游:每个 dispatchable 代理注册为独立子端点。 |
| [mcp](../mcp/) | 下游:每个 dispatchable 代理暴露为 MCP 工具(网络暴露默认拒绝,见 mcp 领域)。 |

## Non-goals

- **用户不能定义自定义代理类型**:代理类型集合(coder/researcher/reviewer + 内部 planner)、skill 集合与 context source 集合都是启动期内置常量,**不是运行期可配置项**。运行期"特化"只能通过 orchestration 的 worker 派生(`spawn_worker` 或 DAG 动态节点:选 base_type + 维度组合),且仍受五档 ToolProfile 与已注册 skill / context source 约束——见 orchestration 领域,不在本领域范围。
- **不开放自定义 Profile**:Full / Review / Edit / ReadOnly / None 五档名称与能力含义固定,不支持用户新增或改写能力档。
- 不做代理、skill、context source 的运行期热插拔/卸载(三个注册表均每次启动构造一次,启动后只读)。
- 不实现 ReAct 循环、上下文构建、工具执行、记忆读写本身(均来自 vage 或归各自领域)。
- 不含 Primary/Fallback Primary 的构造与递归阀门(归 orchestration)。
- 不开放第三方插件向注册表追加描述符(数据结构已支持,但当前未启用)。

## Anti-scenario(绝不能发生)

- **Researcher 绝不写文件、绝不跑 bash**:即便任务隐含"顺手修一下",researcher 预制组合也只能产出"建议",mutation 必须交给持 Full 的执行者(违反则破坏单一写者与能力鸿沟)。
- **Reviewer 绝不写文件**:可跑测试发现问题,但不能直接改。
- **ReadOnly / Review / None 三档绝不出现 write/edit**——即使临时、即使"只改一行",即使 skill 或 system prompt 这么要求(写工具只能来自 Full 或 Edit 档)。
- **skill 绝不隐式带工具**:`review` skill 加到任何组合上,都不得改变该组合的工具面。
- **非 dispatchable 代理(planner)绝不出现在** `delegate_to_*`、HTTP 子端点或 MCP 工具列表中。
- **绝不**在调用点用 if 分支临时给某执行者加发/减工具——能力变更必须经 ToolProfile(描述符声明或 spec 显式声明),否则"某执行者有什么权限"将无法一句话陈述。

## Data dictionary

| 术语 | 语义类型 | 定义 |
|------|---------|------|
| 专家代理 / dispatchable agent | 概念 | 可被 Primary 委派的预制组合:coder / researcher / reviewer(chat/explorer 已移除,Primary 内联承担) |
| 预制组合 | 概念 | 具名的能力维度组合(descriptor + 默认 profile + 默认提示);与派生 worker 的差别只在"是否启动期注册",不在能力表达力 |
| Skill | 概念 | 命名的专项指令片段,追加到系统提示;不授予工具、不放宽权限 |
| ContextSource | 概念 | 命名的只读上下文来源;provider 渲染为显式标注的只读块注入 worker 输入 |
| AgentType | enum | 代理底层实现类型:task / orchestrator,见 dictionary-agent-type |
| ToolProfile | enum/概念 | 命名能力集合;四档预设 Full / Review / ReadOnly / None,见 dictionary-tool-access-level |
| ToolCapability | enum | 能力原子:Read / Write / Execute / Search / Remember,装配阶段翻译为具体工具 |
| 能力鸿沟 | 概念 | 有意制造的能力缺口:ReadOnly/Review 档不能写,迫使一次 mutation 永远经过持 Full 的执行者 |
| AgentDescriptor | 概念 | 代理类型的声明式元数据 + 工厂;注册表元素;"声明一次,多处消费" |
| Dispatchable | enum(bool) | 描述符标志位:是否可作为委派目标 / HTTP 子端点 / MCP 工具暴露 |
