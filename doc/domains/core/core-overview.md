# Core 业务组总览

vv 全部领域归于单一业务组 **core**(MVP 阶段)。本文件是 13 个领域的索引;依赖拓扑见 [../../architecture/domain-dependency.md](../../architecture/domain-dependency.md)。

## 领域索引

| 领域 | 一句话职责 | 暴露 API | 关键不变量 |
|------|-----------|---------|-----------|
| [configuration](configuration/configuration-overview.md) | 配置加载、cwd 捕获、装配中心 | 否 | 配置优先级固定;零成本默认;失败回滚 |
| [tools](tools/tools-overview.md) | 内建工具注册 + 安全护栏 | 否 | 工作区隔离;命令分级;注入扫描 |
| [agents](agents/agents-overview.md) | 专家代理工厂 + 能力分级 | 否 | ToolProfile 决定工具集 |
| [orchestration](orchestration/orchestration-overview.md) | Primary + Dispatcher + DAG 规划 | 否(内部) | 统一前门;递归硬阀门;Primary 不写 |
| [memory](memory/memory-overview.md) | 三层记忆 + 持久化 + 访问控制 | 是(经 http) | 会话私有访问控制 |
| [session](session/session-overview.md) | Session / Plan Workspace / Session Tree | 是(经 http) | 共根删除一致性;写者唯一 |
| [cli](cli/cli-overview.md) | 交互式 TUI + 权限模式 | 否(终端) | 权限模式授权;Allow Always 作用域 |
| [http-api](http-api/http-api-overview.md) | REST/SSE sync/streaming/async | 是 | 预算错误→429;成本富化 |
| [mcp](mcp/mcp-overview.md) | 把代理暴露为 MCP 工具 | 是(MCP) | 网络暴露默认拒绝;凭据过滤 |
| [cost-tracking](cost-tracking/cost-tracking-overview.md) | token / 成本追踪 | 是(经 http) | 三视图同源;仅 USD |
| [budget](budget/budget-overview.md) | session/daily 硬上限 + 软告警 | 是(经 http) | 预算硬阀门 |
| [trace](trace/trace-overview.md) | 结构化事件 JSONL 落盘 | 否 | 零成本默认;异步不阻塞主路径 |
| [eval](eval/eval-overview.md) | 离线评测与质量度量 | 是(经 http,opt-in) | `-eval` 与 `-p`/http/mcp 互斥 |

## 领域分群

- **接口层**:cli、http-api、mcp(三者互斥,单进程单模式)。
- **编排与代理**:orchestration、agents、tools。
- **状态层**:memory、session。
- **可观测与成本**:cost-tracking、budget、trace。
- **装配与质量**:configuration、eval。
