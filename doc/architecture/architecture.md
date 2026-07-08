# 全局架构

> 本文件是 vv 架构的单一来源。源码对照见 vv 仓库的 `CLAUDE.md`。

## 产品定位

vv 是一款"**前门统一、内部分工**"的 AI 代理应用:每次用户请求都进入同一个 **Primary Assistant**,由它自行决定如何回应 —— 直答、只读探查、委派给专家、或多步 DAG 规划。旧的 `intent → execute → summarize` 三段管道已彻底废弃;vv 不再做前置意图分类,所有路由决策由 Primary 通过工具调用完成。

## 分层

```
              ┌─────────── 应用入口 ────────────┐
              │   CLI    │   HTTP   │   MCP    │
              └────┬─────┴────┬─────┴────┬─────┘
                   ▼          ▼          ▼
                       装配中心 (setup)
                          │
           ┌──────────────┼─────────────┐
           ▼              ▼             ▼
        分发器         注册表         记忆/会话
        (Primary +     (代理 +        (memory +
         Dispatcher)   工具能力)        session/tree)
                          │
                          ▼
                   vage 框架 + aimodel SDK
```

- **应用入口层**:只负责命令行解析与运行模式选择,不持有业务逻辑(领域 `cli` / `http-api` / `mcp`)。
- **装配中心(setup)**:所有可选子系统的唯一接线点,决定哪些组件被构造、按什么顺序拼装(领域 `configuration`)。
- **分发器**:对外单一 `agent.StreamAgent`;对内只做"转发到 Primary 或 Fallback"(领域 `orchestration`)。
- **注册表**:把"有哪些代理类型""它们能用哪些工具"建模为数据结构(领域 `agents` / `tools`)。
- **记忆/会话**:可选子系统,按配置选择性挂载(领域 `memory` / `session`)。

## 五条设计原则

这五条是贯穿所有领域的 **架构不变量**,在 `constitution.md` § 5–6 被固化为硬约束。

### 1. 统一前门

对外只暴露一个分发器;策略/路由由 Primary 自己以工具调用方式承担。收益:上层无需为每种代理写入口;新增专家只是给 Primary 多挂一个 `delegate_to_*` 工具;失败回退路径单一。

### 2. 能力分级(ToolProfile)

| Profile | 能力 |
|---------|------|
| Full | 读 + 写 + 执行 + 搜索 |
| Review | 读 + 搜索 + 执行(不允许写) |
| ReadOnly | 读 + 搜索 |
| None | 无工具 |

同一个 Factory + 不同 profile 产出多种代理;新增工具只需归类到能力维度,不必逐个代理改。

### 3. 递归预算(硬阀门)

Primary → 子代理 → 可能再触发 Primary。为避免无限循环,所有委派路径经 `context` 携带递归深度:分发器入口统一检查上限;达到上限强制切换到 **无工具的 Fallback Primary**,物理上消除再次递归的可能。这是"硬阀门",比 try/limit 计数更可靠。

### 4. 零成本默认路径

trace / session / session_tree / budget / debug 都遵循同一规则:**未启用 → 不构造 → 零开销**;启用时才挂到统一事件总线。默认安装既快又简单,按需打开高阶能力。

### 5. 协议无关

所有 LLM 流量经 `aimodel`,OpenAI 与 Anthropic 在 SDK 层规一化为统一接口。换 provider 不动业务代码。

## 一次请求的生命周期

1. 请求进入应用入口,初始递归深度 0。
2. 分发器检查深度:超上限 → Fallback Primary(无工具);否则 → Primary。
3. Primary 跑 ReAct 循环,每轮挑一个动作(直答 / 只读探查 / 委派 / 规划 / 记笔记 / 更新树)。
4. 选"委派"或"规划"时,对应工具先把递归深度 +1,再调子代理或启动 DAG;子代理结果以工具结果回到 Primary,被折叠为最终回复。
5. 全过程事件经统一事件总线分发给:流式输出(SSE/TUI)、可选子系统(trace / session / budget / debug)。

详见领域 `orchestration` 的 spec 与 design。

## 装配阶段(setup)

装配中心是配置与运行期组件之间的唯一桥梁,"按配置组合不同子系统"的复杂度集中于此:

```
配置就绪 → ① 路径与运行环境 → ② LLM 客户端(中间件链:debug → budget)
→ ③ 记忆子系统 → ④ 事件总线 + 可选 hook(trace/session)
→ ⑤ Plan Workspace → ⑥ Session Tree → ⑦ 代理装配 → ⑧ 分发器组装
```

关键约束:

- **单一构造点**:每个组件只构造一次,其余模块依赖注入拿引用。
- **顺序明确**:事件总线必须在 hook 注册前,记忆管理器必须在代理工厂前。
- **失败回滚**:每阶段失败回滚已开资源,半成品不泄露到运行期。
- **可替换接缝**:上层经 Options 注入少量回调(权限拦截、ask_user 实现、debug sink)。

### LLM 中间件链

```
LLM 客户端 └─ debug(debug=true 时) └─ budget(任一 tracker 启用时) └─ 实际调用
```

routing 启用时另构造独立 router LLM 客户端,指向更便宜的小模型。

### 工具装饰链(顺序关键)

```
原始 ToolRegistry(按 ToolProfile)+ ask_user / todo_write 注入
  ↓ 权限拦截(仅 CLI;必须最内层 —— 需看原始工具名做策略匹配)
  ↓ 长度截断(tool_output_max_tokens > 0)
  ↓ debug 装饰(必须最外层 —— 记录代理实际收到的已截断结果)
```

## 运行模式

| 模式 | 适用场景 | 特点 |
|------|---------|------|
| **CLI**(默认) | 终端交互 | Bubble Tea TUI,交互工具走对话框 |
| **HTTP** | 远程客户端 / Web 前端 | REST + SSE,无终端,交互走异步回调 |
| **MCP** | 上游 LLM IDE | 把代理暴露为 MCP 工具,自身不直接对话 |

`-p` 单提示与 `-eval` 评测是 CLI 系下跑一次即退出的短期模式。三模式 **互斥**(单进程不可同时跑)。模式选择遵循 CLI 标志 > 环境变量 > 配置文件 > 默认值。

## 与 vage 的边界

- vage 提供 TaskAgent、内存抽象、ContextBuilder、工具实体、事件总线、session/workspace/tree 持久化、DAG orchestrate。
- vv 的工作是 **挑选、组合、配置**:决定哪些代理存在、哪些工具可用、哪些子系统挂上、外部协议如何映射。
- 反向约束:vv 不向 vage 注入业务概念,避免基础库被特定应用形态绑死。

## 架构决策记录

架构上重大的取舍记录在 [adr/](adr/),已起草 8 个 ADR(0001–0008,当前 `Status: proposed`,见 [adr/adr.md](adr/adr.md)):统一前门替代三段管道、递归深度硬阀门、能力分级 ToolProfile、共根会话目录、事件总线旁路订阅 + 零成本默认、纯 Go SQLite 后端、会话私有记忆访问控制、预算硬阀门 + 软告警。
