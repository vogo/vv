# configuration 领域总览

- **领域名 / 组**:configuration / core
- **一句话职责**:配置加载(CLI > env > YAML > 默认)与装配中心(setup)——配置与运行期组件之间的唯一桥梁。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:无上游领域依赖;被所有其他领域依赖(它构造它们)。
- **API exposure**:false(无对外 HTTP/MCP 端点;通过 InitResult/Options 句柄供进程内消费)

## 关联候选 ADR

- 配置来源优先级链固定为 `CLI > env > YAML > 默认`(参见 design.md「配置来源优先级」)。
- 装配中心作为单一构造点 + 零成本默认(未启用子系统不构造)。
- Shutdown 使用独立 3s 超时上下文,与主上下文解耦。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、不变量(CONFIG-R*)、启动生命周期、Anti-scenario |
| [design.md](design.md) | 优先级链、模式分派、装配 8 阶段、中间件/装饰链、契约 |
| [models.md](models.md) | Configuration 实体、InitResult 聚合句柄、Options |
