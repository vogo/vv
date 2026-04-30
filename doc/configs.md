# 配置体系

## 设计目标

vv 的配置面板很大（LLM、工具、代理、会话、记忆、追踪、预算……），但通过两条原则维持可管理性：

1. **统一来源优先级**：所有字段都遵循 `CLI > env > YAML > 默认值`。
2. **零配置可启动**：除了 LLM API key 必须给（首次启动向导收集），其他子系统都有合理默认或干脆默认关闭。

## 来源优先级

```
最终值 = CLI 标志（若给出）
      ?? 环境变量（VV_* 系列）
      ?? YAML 文件（~/.vv/vv.yaml）
      ?? 程序内默认值
```

环境变量主要覆盖三类：

- 凭据（API key、auth token），避免写到磁盘。
- 子系统开关（trace / session / tree / debug），便于临时启用/关闭。
- 会话与目录路径，便于 CI/CD 隔离。

## 子系统开关与默认状态

| 子系统 | 默认 | 开关 | 是否需要其他子系统 |
|--------|------|------|-------------------|
| LLM | — | 必填 | — |
| Memory（持久化） | 开 | backend 选择 file/sqlite | — |
| Session | 开 | `session.enabled` | — |
| Plan Workspace | 跟随 session | 无独立开关 | session 必须开 |
| Session Tree | 关 | `session_tree.enabled` | session 必须开 |
| Trace | 关 | `trace.enabled` | — |
| Budget | 按需 | 任一硬上限非零即开 | — |
| Debug | 关 | `--debug` / `VV_DEBUG` | — |
| Web Search | 按需 | provider + 凭据 | — |
| MCP Server | 跟随 mode | `mode: mcp` | — |
| Eval | 关（HTTP 端点）；CLI `-eval` 始终可用 | — | — |

依赖关系在装配阶段被显式校验——例如 `session_tree.enabled=true` 而 `session.enabled=false` 会启动报错而非沉默忽略。

## 校验时机

配置在 `Load` 阶段经过三层处理：

1. **解析**：YAML 反序列化 + 严格未知字段处理。
2. **默认填充**：缺省项填默认值（窗口尺寸、并发上限、超时等）。
3. **显式校验**：枚举值（permission mode、memory backend、orchestrate mode）必须在合法集合内；冲突字段（如评测器名）启动期报错。

废弃配置项（早期管道留下的）走"silent ignore + slog.Warn"模式：不阻塞启动，但日志里清晰提示"这个键被忽略了，请删除"。这种处理避免配置升级带来的破坏性变更。

## 项目级提示

`<workdir>/VV.md` 是 vv 约定的项目级提示文件。其内容会被读入并附加到每个代理的系统提示尾部，用于：

- 项目特有的代码规范、构建命令、测试入口。
- 项目特定的安全约束（"禁止访问目录 X"）。
- 项目惯例（"每次改完代码后跑 make lint"）。

VV.md 不参与 YAML 序列化；它是运行时一次性读入的纯文本。

## 凭据安全

API key 类字段：

- 永远不写日志。
- 在配置文件中允许保存，但环境变量优先级更高，便于在共享机器上不落盘。
- 首次启动向导写入 YAML 时会调整文件权限到 600。

## 演化路径

未来可能演化的方向：

- **多环境配置**：当前一个文件，未来可能引入 `vv.<profile>.yaml` 让用户在 dev/staging/prod 之间切换。
- **远程配置**：某些团队希望从中央 git 仓库拉取配置，目前不支持，但配置加载层是单一入口，扩展空间充足。
- **配置版本化**：当前所有字段平铺；未来若做大改可引入顶层 `version` 字段做迁移。

详细字段语义请直接看 YAML 注释与 `vv.yaml` 示例。
