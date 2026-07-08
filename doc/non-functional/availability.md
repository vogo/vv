# 非功能需求:可用性与韧性

vv 是单进程应用(非分布式),可用性聚焦 **优雅降级、失败隔离、优雅关闭**,而非多副本冗余。

## 失败隔离

| 场景 | 行为 |
|------|------|
| 子代理失败 | 以 `IsError=true` 工具结果返回,不冒泡为 Run abort;Primary 基于错误继续决策(改派 / 直答 / 澄清) |
| 并行工具一个失败 | 不取消兄弟工具 |
| 递归失控 | 达深度上限强制切 Fallback Primary(无工具,最大迭代 1),保证有限步骤内必定回应 |
| Session Tree 镜像失败 | 仅记 `slog.Warn`,不阻塞 DAG 执行(树是辅助视图,非关键路径) |
| Trace 落盘失败 | 满通道非阻塞丢弃;consumer 侧 marshal/open/write 错误 `slog.Warn + continue`,绝不 abort goroutine |
| 装配阶段失败 | 回滚已开资源(关 store、关事件总线),半成品不泄露到运行期 |

## 优雅关闭

| 要求 | 约束 |
|------|------|
| 触发 | SIGINT / SIGTERM 或正常完成 |
| 上下文解耦 | Shutdown 用 **独立 3s 超时上下文**,不被已取消的主上下文牵连,保证异步 hook / store 写完最后一批 |
| 覆盖范围 | 关事件总线(trace/session 异步 hook flush)、关 memory store(SQLite 需 close)、关 debug sink |
| 全模式覆盖 | CLI / `-p` / `-eval` / HTTP / MCP 每种模式退出前都调用 |
| 幂等 | `sync.Once` 保证 Stop 幂等 |

## 配置韧性

| 要求 | 约束 |
|------|------|
| 启动期校验 | 必填字段、值域校验失败 → 启动报错,不带病运行 |
| 依赖强校验 | Session Tree 要求 Session 已开;非交互模式(HTTP/MCP/`-p`)缺 API key 直接退出而非阻塞等输入 |
| 首次启动向导 | 仅交互式 CLE 模式;收集最小必要配置写回 YAML |
| 配置优先级 | CLI 标志 > 环境变量 > 配置文件 > 默认值 |

## 预算与降级

| 要求 | 约束 |
|------|------|
| 预算硬阀门 | 超 session/daily 上限在 LLM 调用到达网络前拒绝;HTTP→429 |
| daily 计数 | 进程内,UTC 00:00 滚动,重启清零(有意取舍:避免跨进程协调) |
| 软告警 | 首次越过 `warn_percent × limit`(默认 80%)发一次性 `EventBudgetWarn` |

## 数据持久化韧性

| 项 | 约束 |
|------|------|
| 共根删除一致性 | Session/Workspace/Tree 共目录,`DELETE` 一次递归删除清掉全部,不留孤儿 |
| 记忆后端切换 | file→sqlite **不自动迁移**:启新库,旧 JSON 树原样保留,可切回 |
| SQLite 配置 | WAL 模式、`synchronous=NORMAL`、`busy_timeout=5000`、`foreign_keys=ON` |
| 单进程单会话 | 跨进程同会话 id 会交错写共享文件 —— 单进程单会话是受支持模型 |

## 可用性的负空间(Non-Goals)

- **不** 做多副本 / 多机冗余、灾备(DR)、分布式执行。
- **不** 做跨进程会话合并。
- **不** 做 daily 预算计数的重启持久化。
- **不** 做实时成本告警 / 通知(仅软告警事件)。
