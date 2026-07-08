# agents — Domain Spec

## Overview

agents 领域定义 vv 的**专家代理(specialist / dispatchable agent)**:它们是被前门(Primary)委派去完成具体子任务的 ReAct 循环代理。本领域的核心命题是 **能力分级(capability tiering)**——代理可用的工具不写死在调用点,而由代理描述符声明的 ToolProfile 决定。同一个 Factory 配上不同 profile 即产出不同能力面的代理。

**范围**:专家代理的类型集合、每类的职责边界与工具能力面、ToolProfile 四档分级、代理描述符(AgentDescriptor)与注册表所建模的不变量。

**边界**:本领域**不含** Primary / Dispatcher 编排、递归阀门、DAG 规划与执行(归 [orchestration](../orchestration/))。它只提供编排所消费的专家代理与 profile 模型。具体工具实体与护栏归 [tools](../tools/);代理的实例化时机与依赖来自 [configuration](../configuration/) 的装配中心。

术语见 [../../../glossary.md](../../../glossary.md);代理类型枚举见 [dictionary-agent-type](../../../../vv-prd/dictionaries/core/dictionary-agent-type.md),工具访问级别见 [dictionary-tool-access-level](../../../../vv-prd/dictionaries/core/dictionary-tool-access-level.md)。

## Core entities

| 实体 | 职责 | 详见 |
|------|------|------|
| AgentDescriptor | 单一代理类型的声明式元数据:id / 显示名 / 描述 / ToolProfile / 系统提示 / 工厂函数 / 是否 dispatchable。注册表的元素。 | [models.md](models.md) |
| AgentType | 代理底层实现类型枚举:`task`(ReAct 循环,带可选工具)/ `orchestrator`(任务理解与分发,归 orchestration) | [models.md](models.md)、[dictionary-agent-type](../../../../vv-prd/dictionaries/core/dictionary-agent-type.md) |
| ToolProfile | 命名的能力集合(Capabilities ⊆ {Read, Write, Execute, Search});四档预设 Full / Review / ReadOnly / None | [models.md](models.md)、[design.md](design.md) |
| 专家代理(coder / researcher / reviewer) | 三类按职责分工、全部 dispatchable 的 task 代理 | 本文「专家分工表」、[models.md](models.md) |

> **实现说明(以代码为准)**:实际注册的 dispatchable 专家**只有三类**——coder / researcher / reviewer(见 `vv/setup/setup.go` 的 `RegisterCoder/Researcher/Reviewer`)。早期的 `chat`(无工具纯对话)与 `explorer`(只读探查)代理已**彻底移除**:闲聊由统一 Primary **无工具内联直答**承担,探查由 Primary 用自身的 read/glob/grep 工具完成,故不再有 `delegate_to_chat` / `delegate_to_explorer`(`setup.go` 委派工具家族刻意只含 coder/researcher/reviewer)。此外注册表还含一个 `planner` 代理(ProfileNone,**非 dispatchable**,仅用于内部分类),以及 Primary / Fallback Primary(非 dispatchable)。ToolProfile=None 这一能力档仍存在,由 planner 与 Fallback Primary 例示,而非 chat。

完整属性表引用 [model-agent](../../../../vv-prd/models/core/agents/model-agent.md);角色责任与权限矩阵引用 [roles.md](../../../../vv-prd/architecture/roles.md)。

## 专家分工表

| 代理 | 职责 | ToolProfile | 能力面 | Dispatchable | 委派工具 |
|------|------|-------------|--------|--------------|----------|
| **Coder** | 编码专家:读 + 写 + 执行 + 搜索,唯一持有写工具的角色 | Full | read · write · execute · search | 是 | `delegate_to_coder` |
| **Researcher** | 研究员:只读探查、读文档、抓公网资料,绝不动文件系统 | ReadOnly | read · search | 是 | `delegate_to_researcher` |
| **Reviewer** | 评审员:读 + 搜索 + 跑测试/lint(bash),但不能写 | Review | read · search · execute | 是 | `delegate_to_reviewer` |

> 纯对话与只读探查不再是独立代理:由统一 Primary 内联承担(见上「实现说明」)。`planner`(ProfileNone)是内部分类代理,非 dispatchable,不出现在委派工具家族中(AGENTS-R6)。

分工背后的"能力鸿沟"设计取舍见 [design.md](design.md)「专家代理的能力分工」。各代理系统提示词全文承载于代码(`vv/agents/{coder,researcher,reviewer}.go`),此处不复述。

## Business rules(不变量)

| ID | 规则 | 说明 |
|----|------|------|
| AGENTS-R1 | ToolProfile 决定工具集 | 代理可用工具**不在调用点硬编码**,完全由描述符声明的 ToolProfile 在装配阶段翻译而来(Capability → 具体工具)。同一 Factory + 不同 profile 产出不同能力面的代理。对应候选 ADR-0003。 |
| AGENTS-R2 | Researcher 无写无 bash | researcher = ProfileReadOnly(read + search),**绝无** write / edit / bash。它读到的代码不能改,发现的问题只能反馈。 |
| AGENTS-R3 | Reviewer = Review 能力档 | reviewer = ProfileReview(read + search + execute),可跑测试/lint,但**无写工具**;它的输出是"建议下一步",由 Primary 决定是否派给 Coder。 |
| AGENTS-R4 | Coder 是唯一写者 | 仅 ProfileFull(= coder)持有 write/edit。任何文件 mutation 都必须经过 coder——保留单一写入责任人(与 orchestration 的"Primary 不写"约束互补)。 |
| AGENTS-R5 | ProfileNone 代理无工具 | ProfileNone 代理(`planner`、Fallback Primary)LLM-only,不挂任何工具(含 `ask_user` / `todo_write`)。闲聊由 Primary 同样以无工具方式内联直答。 |
| AGENTS-R6 | 每个 dispatchable 对应一个委派工具 | 注册表中每个 `Dispatchable=true` 的描述符,在 Primary 工具集里自动获得一个 `delegate_to_<id>` 工具,并被 PlannerAgentList 汇入"可委派目标列表"。非 dispatchable 代理(如 planner)永不出现在委派工具家族、HTTP 子端点或 MCP 工具中。 |
| AGENTS-R7 | 描述符声明一次、多处消费 | 一个新代理只需写一个 AgentDescriptor + 一个 Factory,即被工厂装配、Primary 提示拼接、委派工具家族、HTTP 子路由、MCP 暴露五条路径自动看见(具体下游见 design.md)。 |
| AGENTS-R8 | 工具能力代理只读 plan/tree | 专家代理只读 Plan Workspace / Session Tree 视图;写工具只挂给 Primary。避免多写者在同一份 plan.md 上互相覆盖(写者唯一,详见 [session](../session/) 与 [orchestration](../orchestration/))。 |
| AGENTS-R9 | ID 唯一 + 启动期校验 | 注册表 ID 冲突在启动期 panic,不允许运行期出现"半就绪"的代理表。 |
| AGENTS-R10 | 持久记忆仅 Coder | 持久化记忆(PersistentMemory)只注入 coder 的系统提示;其余专家不读持久记忆(对应代码中仅 coder 使用 `NewPersistentMemoryPrompt`)。 |

> 注:能力 → 具体工具的映射表(Read 含公网抓取等)、ToolProfile 四档定义为可从代码恢复的细节,见 [design.md](design.md)「能力 → 工具映射」与 [tools](../tools/) 领域,此处不复述。

## States & transitions

专家代理是**无状态组件**,无生命周期状态机(单次 Run 内的 ReAct 迭代由 vage TaskAgent 管理;执行态由 orchestration 的 Task Plan 跟踪)。注册表本身在装配阶段被填充一次,随后转为只读视图供下游消费——见 [design.md](design.md)「与启动期一次性构造的关系」。

## Domain events

本领域不发布自有业务事件。代理运行期发出的迭代/工具/上下文事件(EventContextBuilt 等)由 vage TaskAgent 经注入的 HookManager 分发,事件归属 [trace](../trace/) 与可观测领域;本领域只负责把 HookManager 经 Factory 注入到代理(见 design.md「HookManager 注入」)。

## Interactions

| 协作领域 | 关系 |
|----------|------|
| [orchestration](../orchestration/) | 消费方:Primary 经 `delegate_to_<id>` 委派 dispatchable 专家;Dispatcher 持有子代理 map;Primary 复用本领域 ToolProfile 模型(默认 ReadOnly,开 bash 切 Review)。 |
| [configuration](../configuration/) | 上游:装配中心创建注册表、注册描述符、按 ToolProfile 构造工具集、提供 Factory 全部依赖(LLM/记忆/护栏/Guard/Hook/IterationStore)。 |
| [tools](../tools/) | 上游:Capability 翻译为具体工具;`ask_user` / `todo_write` 由装配阶段注入工具集;注入 Guard(ToolResultGuards)挂到代理。 |
| [http-api](../http-api/) | 下游:每个 dispatchable 代理注册为独立子端点。 |
| [mcp](../mcp/) | 下游:每个 dispatchable 代理暴露为 MCP 工具(网络暴露默认拒绝,见 mcp 领域)。 |

## Non-goals

- **用户不能定义自定义代理类型**:代理类型集合(coder/researcher/reviewer + 内部 planner)是启动期内置常量,**不是运行期可配置项**。运行期"特化"只能通过 orchestration 的动态规格(DAG step 选 base_type + 自定义提示/工具子集),且仍受四档 ToolProfile 约束——见 orchestration 领域,不在本领域范围。
- 不做代理的运行期热插拔/卸载(注册表每次启动构造一次,启动后只读)。
- 不实现 ReAct 循环、上下文构建、工具执行、记忆读写本身(均来自 vage 或归各自领域)。
- 不含 Primary/Fallback Primary 的构造与递归阀门(归 orchestration)。
- 不开放第三方插件向注册表追加描述符(数据结构已支持,但当前未启用)。

## Anti-scenario(绝不能发生)

- **Researcher 绝不写文件、绝不跑 bash**:即便任务隐含"顺手修一下",researcher 也只能产出"建议",mutation 必须回到 Coder(违反则破坏单一写者与能力鸿沟)。
- **Reviewer 绝不写文件**:可跑测试发现问题,但不能直接改;修复必须经 Coder。
- **任何非 Coder 专家绝不持有 write/edit 工具**——即使临时、即使"只改一行"。
- **非 dispatchable 代理(planner)绝不出现在** `delegate_to_*`、HTTP 子端点或 MCP 工具列表中。
- **绝不**在调用点用 if 分支临时给某代理加发/减工具——能力变更必须经描述符的 ToolProfile,否则"某代理有什么权限"将无法一句话陈述。

## Data dictionary

| 术语 | 语义类型 | 定义 |
|------|---------|------|
| 专家代理 / dispatchable agent | 概念 | 可被 Primary 委派的子代理:coder / researcher / reviewer(chat/explorer 已移除,Primary 内联承担) |
| AgentType | enum | 代理底层实现类型:task / orchestrator,见 dictionary-agent-type |
| ToolProfile | enum/概念 | 命名能力集合;四档预设 Full / Review / ReadOnly / None,见 dictionary-tool-access-level |
| ToolCapability | enum | 能力原子:Read / Write / Execute / Search,装配阶段翻译为具体工具 |
| 能力鸿沟 | 概念 | 有意制造的能力缺口:researcher/reviewer 不能写,迫使一次 mutation 永远经过 Coder |
| AgentDescriptor | 概念 | 代理类型的声明式元数据 + 工厂;注册表元素;"声明一次,多处消费" |
| Dispatchable | enum(bool) | 描述符标志位:是否可作为委派目标 / HTTP 子端点 / MCP 工具暴露 |
