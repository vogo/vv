# configuration — Models

本领域三个实体:`Configuration`(输入)、`InitResult`(装配输出聚合)、`Options`(上层注入接缝)。完整字段清单与每字段语义/默认/env 覆盖见 [model-configuration](../../../../vv-prd/models/core/config/model-configuration.md) 与 `vv/configs/config.go` 的 YAML 注释——此处只写分组语义与关系,不复述字段表。

## 1. Configuration

**用途**:全部运行期设置的唯一只读聚合。由 Load 三层处理(解析→默认→校验)产出,装配后视为不可变快照。顶层按子系统分组。

**关键配置分组**:

| 分组(YAML key) | 语义类型 | 语义 / 约束 |
|------------------|---------|-------------|
| `llm` | reference | LLM provider / model / api_key / base_url。api_key 必填(CONFIG-R4),env 优先(不落盘)。 |
| `server` | text | HTTP 监听地址(默认 `:8080`)。仅 http 模式生效。 |
| `tools` | group | 工具行为:bash 超时(默认 120s)、bash 危险命令分类器(`bash_rules`:enabled + 用户黑/红/白名单正则)、tool_output 截断等。 |
| `agents` | number | 各代理 ReAct 上限与 token 预算(coder/researcher/reviewer 的 max_iterations、token_budget)。 |
| `mode` | enum (run mode) | cli / http / mcp,单进程单选(CONFIG-R8);默认 cli。 |
| `cli` | enum | CLI 专属:`permission_mode`(默认 default;取代废弃 `confirm_tools`,CONFIG-R9)。 |
| `memory` | group | 三层记忆:持久化 backend(file/sqlite,枚举校验)、memory_dir(默认 `~/.vv/memory/`)、session_memory token 预算。默认开。 |
| `orchestrate` | group | Orchestrator/Primary:max_steps(默认 20)、max_parallel(默认 3)、fast_path 启停与阈值/正则。 |
| `context` | number | 上下文窗口管理:model_max_context_tokens(默认 128000)、compression_threshold(默认 0.8)、tool_output_max_tokens(默认 8000)、protected_turns(默认 4)。 |
| `security` | group | 安全边界:MCP 凭据过滤(`mcp_credential_filter`)、工具结果注入扫描等。 |
| `mcp` | group | MCP server 设置;`mode: mcp` 时生效。客户端凭据过滤在 `security` 下。 |
| `eval` | group | 评测子系统;HTTP 端点默认关,CLI `-eval` 始终可用。 |
| `model_pricing` | map(model 模式 → 费率) | 成本估算费率;内置默认 + YAML + `VV_MODEL_PRICING` 合并(后者覆盖前者);查找先精确后最长前缀。 |
| `budget` | group | session/daily token/cost 硬上限;全字段 opt-in(0=禁用),空块 → 零成本基线(CONFIG-R2)。 |
| `trace` | boolean+ | 结构化事件 JSONL 落盘;默认关,`trace.enabled` 启用。 |
| `session` | boolean+ | Session 子系统;默认开,`session.enabled`。Plan Workspace 跟随之(共会话根)。 |
| `session_tree` | boolean+ | Session Tree;默认关,需 `session.enabled=true`(CONFIG-R3),否则启动报错。 |
| `vector` | group | 向量召回子系统;默认关,失败可软降级(soft-fail)。 |
| `debug` | boolean | 详细 LLM/工具 I/O 调试记录;默认 false。CLI > VV_DEBUG > YAML(DEBUG-01)。false 时零行为副作用(DEBUG-02)。 |
| `ProjectInstructions` | text(运行时) | 从 `<workdir>/VV.md` 读入,附加到各代理系统提示尾;`yaml:"-"`,不序列化。 |

**关系**(详见 model-configuration「Relationships」):被 Agent / Tool / CLI Session / Memory / Session Memory / Cost Tracker / Budget 等使用;`model_pricing` 含 Model Pricing 条目;`budget` 含 Budget Config。

## 2. InitResult(装配输出聚合句柄)

**用途**:装配中心返回给启动入口的唯一下游契约。启动入口只通过它(与 Options)与下层交互,不直接持有更下层组件。**任何 nil 字段表示对应子系统未启用,上层须做 nil 检查**(CONFIG-R2)。

**关键属性**(源 `vv/setup/setup.go` `InitResult` / 嵌套 `Result`):

| 属性 | 语义类型 | 约束 / 含义 |
|------|---------|-------------|
| Config | reference | 装配所用的只读 Configuration 快照 |
| LLMClient | reference | 已套中间件链(debug→budget)的 LLM 客户端 |
| MemoryManager / PersistentMem / Compactor | reference | 记忆管理器、持久化记忆、对话压缩器 |
| SetupResult(Result) | reference | 含 Dispatcher、PathGuard/PathGuardian、HookManager(无 hook 特性时 nil)、注册表、子代理 map |
| SessionBudget / DailyBudget | reference | budget tracker;对应硬上限为 0 时 nil |
| SessionStore / Workspace / TreeStore | reference | `session.enabled=false` 时前两者 nil;`session_tree.enabled=false` 时 TreeStore nil |
| VectorStore / VectorEmb | reference | vector 关闭或软降级时 nil |
| IterationStore | reference | 支撑 `--resume` / resume 端点;无 session 时 nil |
| MetricsStore / MetricsHook / BuildReportSink | reference | P0-5 可观测三件套;无 session 时全 nil |
| Shutdown | function | 统一收尾,独立 3s 上下文执行(CONFIG-R12) |

**关系**:Dispatcher → orchestration 领域;SessionStore/Workspace/TreeStore → session;Budget tracker → budget;HookManager → trace/session/metrics 消费者。

## 3. Options(上层注入接缝)

**用途**:启动入口/上层模式(cli/http/mcp)注入装配中心的少量可替换回调与预构造句柄,无需重写装配本身(可替换接缝)。

**关键属性**(源 `vv/setup/setup.go` `Options`):

| 属性 | 语义类型 | 含义 |
|------|---------|------|
| WrapToolRegistry | function(可选) | 包装工具注册表(如 CLI 权限拦截确认);仅 CLI 模式注入,装饰链最内层 |
| UserInteractor | reference(可选) | ask_user 工具的 interactor 实现(TUI/HTTP 回调/非交互 fallback,按模式定,CONFIG-R5/R8) |
| AskUserTimeout | duration(可选) | ask_user 响应超时;非交互模式忽略(立即 fallback) |
| DebugSink | reference(可选) | debug sink;仅 `cfg.Debug=true` 时构造(按模式选 file/stderr/slog) |
| HookManager | reference(可选) | 预构造事件总线,注入每个 TaskAgent |
| Workspace | reference(可选) | 非 nil 时为每个 dispatchable 代理与 Primary 挂 Plan Workspace 工具与 context source;nil 禁用 |
| TreeStore | reference(可选) | 非 nil 时经 SessionTreeSource(只读)暴露给 TaskAgent,Primary 经 tree_* 工具写;nil 禁用 |
| TreePredicate | function(可选) | 按 session 门控 SessionTreeSource(plumb `auto_enable_after_events` 策略);nil 则始终开 |
| VectorStore / VectorEmbedder | reference(可选) | 两者均非 nil 时暴露 vector 召回与 Primary 的 vector_search/vector_add;nil 按调用禁用 |

**关系**:Options 是 cli/http-api/mcp 领域 → configuration 的反向依赖通道;与 InitResult 共同构成装配中心的完整对外契约(详见 [design.md](design.md)「InitResult/Options 契约」)。
