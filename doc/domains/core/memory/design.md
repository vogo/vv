# memory 领域 Design

> 本文件写 **HOW**:三层记忆的技术视图、namespace 策略、磁盘布局、访问控制实现、两种后端、压缩机制与生命周期。业务行为与不变量见 [spec.md](spec.md);字段全清单见 [models.md](models.md)。源码:`vv/memories/`(`filestore.go` / `sqlitestore.go` / `namespaces.go` / `session.go`)。

## 三层视图

vv 直接复用 vage 的记忆模型,把记忆划分为三层视图。代理在每轮 ReAct 循环中看到的是三层经 **组合 + 压缩** 后的视图。

| 层 | 范围 | 寿命 | 由谁管理 |
|---|------|------|---------|
| **Working** | 当前 turn 的全部消息 | 一轮请求 | 运行时(单次 agent run),不落盘 |
| **Session** | 本会话历史 + 抽取的 facts + 摘要 | 一次进程或会话生命周期 | session manager |
| **Persistent** | 跨会话长期记忆(KV / 向量) | 永久(file / sqlite 持久化) | `memory.Store` 后端 |

记忆系统与 session 子系统 **正交**:二者共用同一 vage 记忆抽象,但各有独立存储后端与开关。可按需启用(如希望事件流落盘但不维护长期记忆,适合一次性诊断会话)。

## namespace 策略

记忆按 namespace 分组,分两类(枚举值见 [dictionary-memory-namespace](../../../../vv-prd/dictionaries/core/dictionary-memory-namespace.md)):

- **共享 namespace**:跨会话可见的"长期事实"。约定枚举 = `project` / `user` / `conventions` / `notes` / `default`(`default` 是裸 key 无 `<ns>:` 前缀时的隐式 fallback,保后向兼容)。
- **会话私有 namespace**:不在共享清单中的任意名字(如 `scratch` / `ephemeral` / `draft-skills`),会话级,外部 HTTP 端点不可写。

**设计目的**:共享 namespace 的名字是 **约定而非自由文本**,避免"每个代理自己起名"导致碎片化;同时让 user-path 入口能 **静态判定** 一次写入是否越界(`memories.IsSharedNamespace(ns)` 在到达 store 前预校验)。判定逻辑见 `namespaces.go::isShared`(共享清单 + 可选 extra 集合)。

## 访问控制实现

两类入口经 context 携带不同身份标记(`session.go`):

| 入口 | context 标记 | 可写范围 | 越界行为 |
|------|-------------|---------|---------|
| agent-path(工具/技能写) | `WithSessionID(ctx, sid)` | 共享 + 当前会话私有 | 跨会话读→`not-found`;跨会话改/删→`ErrSessionForbidden` |
| user-path(CLI/HTTP) | `WithUserPath(ctx)` | 仅共享 | 写会话私有→forbidden(403/CLI 消息);Clear 仅此路径 |

- `SessionIDFrom(ctx)` 取当前会话身份;空字符串 = 无代理身份。
- `WithSessionID` 对空 `sessionID` 是 no-op(不污染 context)。
- 违反统一 surface 为 `memories.ErrSessionForbidden`(`errors.Is` 可程序化判定),映射到宪法 § 4 的"记忆访问控制不变量"。
- **legacy 保护**:会话私有布局下 `session_id == ""` 的旧记录视为 legacy shared——任一会话与 user-path 可读,但某会话的 `Set` 不可覆盖它(`filestore.go` Set 显式 guard,返回 forbidden),防止新会话静默吞掉历史。

## 磁盘布局(file 后端)

| 类别 | 路径 |
|------|------|
| 共享条目 | `<memory_dir>/<namespace>/<key>.json` |
| 会话私有条目 | `<memory_dir>/session/<session_id>/<namespace>__<key>.json` |
| 读路径解析 | 先试 per-session 私有路径,回退 legacy 共享路径(`resolveReadPath`) |

每条 entry 一个 JSON 文件,含 `key/namespace/session_id/content/ttl/created_at/updated_at`。namespace 与 per-session 目录在首写时自动创建。key/namespace 拒绝路径分隔符与特殊字符(仅允许字母数字、连字符、下划线)。

## file vs sqlite 后端

由 `memory.backend` 选择,两后端实现同一 `memory.Store` 接口,session/namespace 语义 **完全一致**,切换是配置变更而非行为变更。**不自动迁移**(spec MEM-R7):切换后旧后端数据对新后端不可见。

| 维度 | `file`(默认) | `sqlite`(opt-in) |
|------|--------------|-------------------|
| 存储 | 每条 entry 一个 JSON 文件 | 单 `<memory_dir>/memory.db` + `-wal` / `-shm` sidecar |
| 适用 | 小规模团队/个人;需文件粒度审计、易 grep 调试 | 多代理频繁更新、长时间运行服务;需并发写友好 |
| schema | 目录布局即 schema | 单 `entries` 表,复合主键 `(namespace, name, session_id)`;`session_id=''` 标记共享行 |
| 并发 | 文件级 | WAL 多读者并发 + 单写者由 `busy_timeout` 排队序列化;连接池上界收紧 |
| 选择依据 | 需文件审计 / 简单可读 | 需并发写 |

### sqlite 细节(`sqlitestore.go`)

- **driver**:`modernc.org/sqlite`(纯 Go),理由——宪法 § 2 要求 `CGO_ENABLED=0` 可构建,排除需 CGO 的 `mattn/go-sqlite3`。
- **DSN PRAGMA**:`journal_mode(wal)` + `synchronous(normal)` + `busy_timeout(5000)`。
- **schema 版本**:`PRAGMA user_version = 1`(`sqliteSchemaV1`)。打开时若 DB 的 `user_version` **高于** 本二进制构建版本则拒绝打开,防旧 vv 静默写坏新格式。
- **建表后探针**:`SELECT 1 FROM entries LIMIT 1` 触发 DSN PRAGMA 实际生效并验证表存在。
- **文件权限**:DB 文件及 WAL/SHM sidecar 收紧权限。
- **TTL**:`entries.ttl INTEGER NOT NULL DEFAULT 0`,惰性 on-read 过期,与 file 后端语义一致。

> **迁移策略**:无自动迁移。schema 演进经 `user_version` 单调推进 + 显式迁移代码;file→sqlite 的数据搬运留给将来的显式工具,不在装配路径触发。

## 压缩机制(working / session 管理)

session 层有 token 上限,组合视图交给模型前经多种压缩(spec MEM-R8):

| 机制 | 触发 | 行为 |
|------|------|------|
| session 摘要(滑动窗口) | `token_count > 80%` budget | 早期对话摘要为紧凑文本,保留最新 N 个 protected turns;复用同一 LLM 客户端,输入限制在模型上下文 80% 以内避免摘要请求自身溢出 |
| 主动 auto-compact | 逼近模型上下文上限 | 主动压缩历史 |
| 反应式 emergency compact | 收到上下文溢出错误 | 兜底压缩后重试 |
| 工具输出截断 | 单个工具结果过大 | 截断进入上下文的工具输出 |
| `/compact`(CLI) | 用户手动 | 手动触发压缩历史 |

摘要失败(LLM error)保留未摘要 facts,下次 exchange 重试;token 估算偏保守,宁可早压缩避免超真实上限。

## 上下文组装

组装代理上下文优先级:**persistent → session 摘要 → 近期 facts**。仅 **Coder** 默认在系统提示中渲染当前全部 persistent 条目(让模型写代码时看到项目级约定);Researcher / Reviewer / Primary 默认不读 persistent,避免每轮无谓 prompt 膨胀,也因其工作不需要长期偏置。

## 生命周期与装配

- configuration 的 **装配中心** 按 `memory.backend` 构造后端与三层 manager(memory 领域依赖 configuration)。
- sqlite 后端的 DB 句柄由 `setup.Init` 持有,经 `Shutdown` 关闭(宪法 § 6「优雅关闭」:与主上下文解耦的独立 3s 超时上下文)。file 后端无长驻句柄。
- 零成本默认:persistent 未启用即不构造后端。

## Vector recall(可选)

Vector Store / Document 提供与 KV 互补的语义召回(top-k cosine)。框架自带 in-process `MapVectorStore`(测试/小规模),并标准化 `VectorStore` 接口供真实后端(qdrant/pgvector/chroma/pinecone)实现。`locked_dimension` 在首个 Add 锁定,后续维度不符返回 `ErrDimensionMismatch`。`VectorRecallSource` 把 top-k 文档渲染为单条 system 消息注入下一轮。框架不强制 namespace,按需"一 scope 一 store 实例"分区。详见 [models.md](models.md)。

## 技术取舍

| 取舍 | 选择 | 理由 |
|------|------|------|
| 默认后端 | file | 零成本默认、可读、易审计;多数用例(个人助手、单团队 bot)足够 |
| sqlite driver | `modernc.org/sqlite` | 保 `CGO_ENABLED=0`(宪法 § 2 构建约束) |
| 后端迁移 | 不自动 | 自动迁移失败面大、价值低;切换是有意识的配置决策 |
| TTL | 惰性 on-read,默认 0 | 无后台扫描开销;自动过期属按需扩展 |
| 压缩 | 默认滑动窗口摘要器 | 复用 session 已有摘要器,零额外 LLM 成本路径 |
| persistent 默认读者 | 仅 Coder | 避免无关代理 prompt 膨胀,长期偏置只给需要的代理 |

## 演化路径(按需扩展,非必须)

- **向量化检索**:当前 KV 按 namespace+key 精确查;大量条目下转向 Vector Store 相似度检索。
- **多用户隔离**:当前单进程共享一份持久记忆;多租户需租户维度。
- **TTL 与容量上限**:当前无自动过期/容量回收,可基于条目时间戳实现。
