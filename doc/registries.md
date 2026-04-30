# 注册表与能力分级

## 设计动机

如果让"代理类型有哪些"和"它们能用哪些工具"这两件事散落在调用点，会产生两类问题：

1. **新增代理需要改多处** —— 调用点、HTTP 路由、Primary 工具列表都得同步。
2. **能力组合不可解释** —— 某代理拥有什么权限要靠读代码归纳，无法用一句话陈述。

注册表把这两件事抽出来，让代理生命周期成为 **声明式**：声明一个描述符，下游所有装配/路由/委派自动跟随。

## 数据模型

```
   AgentDescriptor
   ├── id, displayName, description
   ├── ToolProfile         （声明能用哪些能力）
   ├── 系统提示
   ├── 工厂函数            （拿到完整依赖后产生 agent.Agent 实例）
   └── isDispatchable      （是否被 Primary 通过 delegate_to_* 看见）

   ToolProfile
   ├── name
   └── Capabilities        （read / write / execute / search 子集）
```

注册表在装配阶段被填充一次，然后变成只读视图供下游消费。

## ToolProfile 的四档预设

| Profile | 含义 | 典型代理 |
|---------|------|---------|
| Full | 读 + 写 + 执行 + 搜索 | Coder |
| Review | 读 + 搜索 + 执行 | Reviewer |
| ReadOnly | 读 + 搜索 | Researcher / Primary |
| None | 无工具 | Planner / Fallback Primary |

预设之外允许自定义（动态规格场景），但日常使用应优先映射到这四档以保持一致性。

## 能力 → 工具的映射

每个 Capability 在装配阶段被翻译为具体工具集：

| Capability | 注册的工具 |
|-----------|-----------|
| Read | 读取文件 + 公网抓取 + 可选公网搜索 |
| Write | 文件创建 + 文件 patch |
| Execute | shell 执行（受超时与路径黑名单约束） |
| Search | 文件名 glob + 内容 grep |

注意 **公网检索算"读"**：把它归到 Read 而不是 Search，是因为模型语义上把它当作"获取外部信息"，与"在已知项目里找东西"是不同的认知模式。

## 描述符的下游消费者

声明一次，多处自动消费：

- **代理工厂**：装配中心遍历 dispatchable 描述符，按 ToolProfile 构造工具集，调用工厂得到 agent 实例。
- **Primary 提示词拼接**：`PlannerAgentList` 把每个 dispatchable 代理的 description 汇总成一段"可委派目标列表"，用于 Primary 的提示。
- **委派工具家族**：Primary 工具集内每个 dispatchable 代理自动获得一个 `delegate_to_<id>`。
- **HTTP 子代理路由**：HTTP service 注册每个 dispatchable 代理为独立端点，方便外部直连测试。
- **MCP 工具暴露**：MCP server 把 dispatchable 代理暴露为 MCP 工具。

任何一个新代理只需要写一个描述符 + 一个工厂，就能被以上所有路径自动看见。

## Primary 的特殊路径

Primary 不通过 `Dispatchable()` 列表自动构造，而是装配中心单独处理：

- 它的 ToolProfile 由"是否允许 Primary 直接跑 bash"这个开关决定（默认 ReadOnly，开启 bash 后切到 Review）。
- 在常规工具集之上额外挂载：委派工具家族、规划工具、Plan Workspace 工具（启用时）、Session Tree 工具（启用时）、ask_user、todo_write。
- `Dispatchable=false`，所以它不会出现在 HTTP 子端点列表，也不会被任何 `delegate_to_*` 看见——只能通过分发器进入。

## 与启动期一次性构造的关系

注册表本身是 **每次启动构造一次**，并不是全局单例。这样：

- 不同进程、不同测试可以独立装配出不同的代理集合。
- 注册过程的失败（例如 ID 冲突）在启动期就 panic，避免运行期出现"半就绪"的代理表。
- 测试中可以注入 fake 描述符。

## 演化路径

注册表保留了若干扩展点，未来可能用到：

- **动态描述符**：DAG 中可以根据 step 的 spec 即时构造代理（指定 base type + 自定义提示 + 自定义工具子集），不需要预先注册。
- **第三方代理**：插件机制可以在装配前向注册表追加描述符，目前未启用，但数据结构已经支持。
