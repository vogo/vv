# eval 领域总览

- **领域名 / 组**:eval / core
- **一句话职责**:离线评测与质量度量——对一个 JSONL 数据集逐用例跑分发器(Dispatcher),按多维度评测器打分,输出报告与退出码,用于回归测试与 CI 守门。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:
  - [orchestration](../orchestration/orchestration-overview.md):评测复用真实分发器(`Dispatcher.Run`)执行每个用例的 `RunRequest`,故"评测通过"等价于"产品代码通过"。
  - [configuration](../configuration/configuration-overview.md):`cfg.Eval`(`EvalConfig`)由装配中心加载,提供评测器列表、阈值、并发、超时与 HTTP 门控;`llm_judge` 复用主 LLM client。
- **API exposure**:true(`POST /v1/eval/run`,**opt-in**)。路由仅在 `eval.enabled: true` 时挂载;CLI `-eval` 入口不受该开关影响。

## 关联 ADR

| ADR | 主题 | 状态 |
|-----|------|------|
| — | 默认评测器组合 `[latency, cost]`(零额外 LLM 成本);`llm_judge` opt-in;HTTP 面默认关闭(opt-in 安全姿态);评测器只覆盖过程指标(速度/成本),不内嵌通用"标准答案" | 未立项(行为已稳定,载于本领域 spec/design) |

> 本领域当前无独立 ADR 文件。零成本默认、opt-in 暴露、"过程指标优先于结果指标"等跨切面取舍记录在 [spec.md](spec.md) 的 EVAL-R* 规则与 [design.md](design.md);若日后需冻结为决策记录,在 `architecture/adr/` 预占编号并经宪法修订程序签署。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、业务规则(EVAL-R*)、Domain events、交互契约、Non-goals、Anti-scenario、数据字典 |
| [design.md](design.md) | 数据集模型与执行策略、六评测器表、默认组合零成本理由、CLI vs HTTP 入口、并发/超时控制、退出码、与可观测性的关系、技术取舍 |
| [models.md](models.md) | Eval Dataset、Eval Case、Evaluator、Eval Report / Summary 实体模型 |

## 关联不变量

- `-eval` 评测与 CLI 单提示(`-p`)、`--mode http|mcp` 互斥,见 [glossary.md](../../../glossary.md)「运行模式」小节。
- 评测期间的 LLM/工具事件仍走主事件总线,可观测性(trace / budget / debug)按其各自配置生效,见 [trace](../trace/trace-overview.md)、[budget](../budget/budget-overview.md)。
- 评测器名称枚举属 configuration 校验范畴(`configs.ValidateEval`);未知名在启动期拒绝。
