# 全局测试策略

## 测试层次

| 层次 | 位置 | 依赖 | 关注 |
|------|------|------|------|
| 单元测试 | 与源码同目录 | 无外部依赖 | 工具/代理/记忆/守卫的纯逻辑与不变量 |
| 集成测试 | `vv/integrations/<group>_tests/<scenario>_tests/` | `VV_LLM_API_KEY` 等 | 跨子系统装配、真实 LLM 调用路径 |
| 离线评测(eval) | `-eval <dataset.jsonl>` / `POST /v1/eval/run` | 视 evaluator 而定 | Dispatcher 端到端质量与回归 |

`make build` = format → lint → test 是合并底线(`constitution.md` § 7)。

## 必测的不变量(跨领域)

这些是 spec 级不变量,必须有自动化测试覆盖:

- **递归预算**:达深度上限切 Fallback Primary;委派路径 +1 正确传递。
- **能力分级**:每个 profile 产出的工具集正确(如 researcher 无 write/bash)。
- **工作区隔离**:文件/搜索/bash 工具拒绝越界路径与 symlink 逃逸;硬阻断 `/proc` `/sys` `/dev`。
- **bash 分级**:TierBlocked 硬拒绝;Dangerous 在 HTTP 拒绝 / CLI 确认。
- **注入扫描**:High 严重度无条件 block;log/rewrite/block 三动作正确。
- **MCP 凭据过滤**:4 个扫描点;事件载荷只带掩码;block 时 handler 不被调用 / 远程 CallTool 不分发。
- **记忆访问控制**:会话私有 namespace 跨会话不可读/写/删;user-path 仅限共享 namespace;`ErrSessionForbidden`。
- **预算硬阀门**:超限在网络调用前拒绝;`errors.Is(err, ErrBudgetExceeded)`;HTTP→429;软告警一次性。
- **todo 不变量**:≤1 个 in_progress;列表上限 100;`null`/`[]` 清空;version 单调递增。
- **并行工具事件顺序**:ToolCallStart 按序 → 并行 → ToolCallEnd/result 按序;错误隔离。
- **零成本默认**:子系统未启用时不构造(可用计数/句柄断言验证)。
- **优雅关闭**:全模式退出前 flush;Stop 幂等。

## evaluator 清单

6 个 evaluator:`latency`、`cost`、`contains`、`llm_judge`(框架层)+ `exact_match`、`tool_call`(vage/eval 程序化)。默认组合 `[latency, cost]`(零额外 LLM 成本);`llm_judge` opt-in 复用主 LLM client。详见领域 `eval`。

## anti-scenario 测试(负空间)

每个核心领域的 feature spec 必须含至少一条 anti-scenario(绝不能发生),并有对应自动化测试。例:Primary 不得直接写文件;预算超限不得放行 LLM 调用;会话不得读到其他会话私有记忆;凭据明文不得进入事件/trace/日志。
