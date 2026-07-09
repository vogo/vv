# session 领域总览

- **领域名 / 组**:session / core
- **一句话职责**:把三个 vage 子系统(Persistent Session、Plan Workspace、Session Tree)组合到同一会话目录根下,提供跨进程、跨 run 的持久化任务状态与结构化记忆,并保证删除一致性。
- **Identity**:vv 端的"会话"一等公民。会话由字符串 `session_id`(`^[A-Za-z0-9._-]{1,128}$`)寻址;同一 id 在元数据/事件流、Plan Workspace、Session Tree 三套状态间共享。
- **Ownership**:平台/核心运行时
- **Status**:active
- **Dependencies**:上游依赖 [configuration](../configuration/configuration-overview.md)(装配中心构造 SessionStore / Workspace / TreeStore 并强校验启用关系);复用 vage 的 `session`、`workspace`、`session/tree` 三个包提供底层协议。无其他领域上游依赖。
- **API exposure**:true。经 [http-api](../http-api/http-api-overview.md) 暴露 `/v1/sessions/*`(会话列表/详情/事件、Plan Workspace 文件读取、Session Tree 节点 CRUD 与折叠);亦经 [cli](../cli/cli-overview.md) 提供列出/恢复/打印 tree。本领域不直接持有 HTTP 路由,仅提供被查询的 store 句柄。

## 关键不变量(摘要)

| 不变量 | 一句话 |
|--------|--------|
| 共根删除一致性 | 三子系统共目录根,`DELETE` 一次递归删除清掉全部,不留孤儿状态(constitution § 4) |
| 写者唯一 | Plan Workspace 只有 Primary 能写,专家代理只读(constitution § 5) |
| 启用关系强校验 | Session Tree 要求 Session 已开,装配阶段报错(constitution § 6) |

完整规则见 [spec.md](spec.md)(SESS-R*)。

## 关联候选 ADR

- **ADR 0004 共根会话目录** —— Session / Plan Workspace / Session Tree 共目录根,删除一致性自动达成。本领域的核心架构决策。详见 [../../../architecture/adr/adr.md](../../../architecture/adr/adr.md) 候选清单(尚未正式落盘,需用户批准)。
- 关联 **ADR 0005 事件总线旁路订阅 + 零成本默认** —— Session 事件经异步 hook 旁路落盘;子系统未启用即不构造。

## 领域文档索引

| 文档 | 内容 |
|------|------|
| [spec.md](spec.md) | 业务行为、核心实体、不变量(SESS-R*)、折叠状态机、领域事件、Non-goals、Anti-scenario、数据字典 |
| [design.md](design.md) | 三子系统共根目录、启用关系表、元数据/事件流分离 + 异步 hook、Plan Workspace 协作语义、折叠器三档 + 触发 Any-of、auto-enable 门控、写树镜像、CLI/HTTP 入口、共根删除、技术取舍 |
| [models.md](models.md) | Persistent Session、Plan Workspace、Session Tree、Tree Node 实体模型 |

## 关联文档

- 术语:[../../../glossary.md](../../../glossary.md);共根删除一致性见 [../../../constitution.md](../../../constitution.md) § 4
