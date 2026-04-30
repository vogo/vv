# 启动入口与生命周期

## 角色定位

启动入口的唯一职责是 **解析意图 → 选择运行模式 → 调用装配中心 → 移交控制权**。它不持有任何业务逻辑，也不做组件构造，只是命令行参数与子系统启动函数之间的薄胶水层。

## 配置来源优先级

每个配置项都遵循同一条优先级链：

```
CLI 标志  >  环境变量  >  配置文件 (~/.vv/vv.yaml)  >  默认值
```

启动入口负责实现这条链：解析标志、读取环境、加载文件、按需触发首次启动向导。

## 首次启动向导

当配置文件不存在或缺关键字段（API key 等），交互模式下进入向导收集最小必要配置并写回 YAML。非交互模式（HTTP / MCP / `-p`）则直接退出报错——这些场景不能阻塞等待用户输入。

## 模式分派

按 `mode` 决定后续路径：

| 模式 | 入口 | 输入 | 输出 |
|------|------|------|------|
| `cli` | 交互式 TUI | 标准输入 + 终端事件 | 终端输出 |
| `cli` + `-p` | 单提示 | 命令行参数 | stdout 结果 + stderr 诊断 |
| `cli` + `-eval` | 评测 | JSONL 数据集 | 报告 + 退出码 |
| `http` | REST/SSE 服务 | HTTP 请求 | HTTP 响应 |
| `mcp` | MCP 服务 | stdio 或 Streamable HTTP | MCP 协议消息 |

后三类（`-p` / `-eval` / HTTP / MCP）必须使用 **非交互式 ask_user 实现**——没有终端可以弹问题，所以遇到 ask_user 工具调用时直接失败（HTTP 模式下另有异步回调机制，详见 [httpapi.md](httpapi.md)）。

## 互斥规则

- `-p` 与 `-eval` 互斥；两者都禁止与 `mode: http`/`mode: mcp` 同时使用。
- `--session list` / `--tree` 仅 CLI 模式可用——HTTP 用户应直接调用对应 REST 端点。
- `--debug` 在不同模式下选择不同的输出 sink（详见 [observability.md](observability.md)）。

## 生命周期

```
进程启动
   │
   ▼
解析 → 加载 → 校验 → (可选) 向导 → 装配
   │
   ▼
进入运行模式（阻塞）
   │
   ▼
SIGINT/SIGTERM 或正常完成
   │
   ▼
独立超时上下文下执行 Shutdown：
  · 关闭事件总线（trace/session 异步 hook 落盘）
  · 关闭 memory store（SQLite 后端需要 close）
  · 关闭调试 sink
```

关键设计：**Shutdown 上下文与主上下文解耦**。主上下文在 SIGINT 那一刻已被取消，如果用它收尾，异步 hook 与 store 来不及写完最后一批事件。所以使用一个 3 秒独立超时上下文，既保证有限时间内完成，又不被外层取消牵连。

## 与装配中心的契约

启动入口只通过装配中心暴露的两个对象与下层交互：

- **InitResult**：包含分发器引用、可选子系统句柄（session store / workspace / tree store / budget tracker 等）、统一的 Shutdown 函数。
- **Options**：启动入口可注入的少量回调，主要是"是否包一层 CLI 权限拦截"和"使用哪种 ask_user interactor"。

任何模式下，启动入口都不应该直接持有 LLM 客户端、记忆管理器、代理工厂等更下层的组件——这是装配中心的实现细节。
