# vv 项目宪法(Constitution)

本文件是 vv 的 **最高约束文档** —— 几乎不变的原则,定义任何 spec、design 或代码都不得越过的红线。它位于 feature spec、代码、个人偏好之上。每条都应能对"是否被违反?"回答 Yes/No。

> 修订需显式干系人签字 + 通知期(见 § 修订程序)。本文件须与当前代码现实一致;若某条与现状冲突,附整改计划而非假装合规。

## 1. 使命与价值序

**使命**:把 vage 框架的可组合能力装配成一个安全、可观测、成本可控的生产级 AI 编码代理。

**价值优先序**(冲突时高位优先):

1. **安全与隔离** > 功能丰富 —— 宁可少一个能力,不可让代理越出工作区或泄露凭据。
2. **数据正确性** > 性能 —— 计费、预算、记忆访问控制的正确性高于延迟。
3. **可解释 / 可观测** > 自动化程度 —— 代理行为必须可追踪、可复盘。
4. **零成本默认** > 能力完备 —— 默认安装必须快且简单,高阶能力按需开启。

冲突裁决:高位价值胜出;无法判定时升级到架构评审。

## 2. 技术栈基线

- **允许的核心栈**:Go;LLM 流量一律经 `aimodel`(OpenAI / Anthropic 在 SDK 层规一化);代理能力一律经 `vage` 框架。
- **禁止**:在业务代码中直接调用某 provider 的原生 SDK,绕过 `aimodel`(违反"协议无关")。vv 不得向 `vage` 注入 vv 专属业务概念。
- **构建约束**:必须保持 `CGO_ENABLED=0` 可构建(故 SQLite 用纯 Go 的 `modernc.org/sqlite`)。
- **例外流程**:引入新核心依赖或新 provider 需 ADR + 架构评审。

## 3. 安全基线(不可协商)

- **工作区隔离**:所有文件工具(read/write/edit via `os.Root`;glob/grep via canonical 路径校验)与 bash 路径参数必须受工作区 allow-list 约束;硬阻断 `/proc` `/sys` `/dev`。
- **命令分级**:bash 命令必须经风险分类器;TierBlocked 在工具内部硬拒绝,Dangerous 在 HTTP 模式拒绝、CLI 模式确认。
- **注入防御**:每个工具结果在进入模型上下文前必须被注入扫描;High-severity 结构性攻击无条件升级为 block。
- **凭据不泄露**:MCP I/O 边界必须扫描凭据;事件载荷只带掩码预览(如 `AKIA****`),绝不带明文;API key 只存在于出站 HTTP 请求,不进入 envelope / trace / 日志。
- **无硬编码密钥**:API key 经配置 / 环境变量注入,不入库、不入 trace。
- **MCP 网络暴露默认拒绝**:任何非 loopback 绑定(含裸 `:port`)在未设 `auth_token` 时启动期拒绝;认证用常量时间比较。
- **特权操作可审计**:破坏性 / 外发操作经事件总线发出可观测事件。

## 4. 数据与一致性基线

- **记忆访问控制是不变量**:会话私有 namespace 条目绑定 `session_id`,跨会话不可读/写/删;违反 surface 为 `ErrSessionForbidden`。CLI/HTTP user-path 仅限共享 namespace。
- **预算是硬阀门**:超过 session/daily 上限必须在 LLM 调用到达网络前拒绝(`errors.Is(err, ErrBudgetExceeded)`);HTTP surface 为 429。
- **计费正确性**:成本估算基于可配置价格表;仅 USD;token 统计来自同一份 LLM 中间件数据,三种成本视图差别只在累加边界。
- **会话删除一致性**:Session / Plan Workspace / Session Tree 共用目录根,`DELETE` 一次递归删除清掉全部,不留孤儿状态。

## 5. AI 工程原则

- **统一前门**:对外只暴露一个 `agent.StreamAgent`;路由由 Primary 以工具调用承担;新增专家 = 给 Primary 多挂一个 `delegate_to_*` 工具,不改入口。
- **能力分级,不硬编码**:代理可用工具由 ToolProfile 声明(Full/Review/ReadOnly/None),不写死在代理代码里。
- **递归有硬上限**:所有委派路径经 context 携带递归深度;超限强制切无工具 Fallback Primary —— 物理阀门,不依赖计数自觉。
- **Primary 不得直接 mutation**:Primary 无写工具,一切文件修改必须经 `delegate_to_coder`。
- **写者唯一**:Plan Workspace 只有 Primary 能写,避免多专家并发覆盖。
- **失败不冒泡为 abort**:子代理失败以 `IsError=true` 工具结果返回,让上层基于错误继续决策。

## 6. 可观测与运维原则

- **不污染主路径**:所有计数与落盘在事件总线之外旁路进行;主路径只发事件。
- **可独立开关 + 零成本默认**:trace / session / tree / budget / debug 正交,未启用即不构造。
- **优雅关闭**:Shutdown 必须在与主上下文 **解耦** 的独立超时上下文(3s)下执行,保证异步 hook / store 写完最后一批;每种模式退出前都调用。
- **配置错误启动期暴露**:子系统依赖(如 Session Tree 要求 Session 已开)在装配阶段强校验,不沉默忽略。
- **配置优先级固定**:CLI 标志 > 环境变量 > 配置文件 > 默认值。

## 7. 工程原则

- **WHAT/HOW 分离**:`spec.md` 写业务行为,`design.md` 写实现;`doc/` 写代码无法表达的意图与边界,不复述代码逻辑。
- **测试底线**:`make build` = format → lint → test 必须通过;单元测试无外部依赖;集成测试隔离在 `integrations/`。
- **函数式选项**:工具/代理配置统一用 functional options 模式。
- **context 贯穿**:一切跨函数边界的操作都经 `context.Context` 传递。
- **文档中文为主**,技术术语保留英文。

## 修订程序

- **提案人**:任何核心维护者可提 amendment。
- **签字门槛**:涉及 § 1–4(价值/技术/安全/数据)的修订需架构负责人 + 安全负责人双签;§ 5–7 需架构负责人单签。
- **通知期**:合并前公示。
- **违规处理**:违反 § 3 安全基线的变更一律拒绝合并,无例外豁免。
- **审查周期**:每年复审一次;频繁改动意味着把细则误当原则,应下沉到领域 spec 或 `shared/`。
