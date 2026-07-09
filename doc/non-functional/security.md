# 非功能需求:安全

安全是 vv 的最高价值(`constitution.md` § 1)。本文件量化跨领域的安全要求;具体实现见 `domains/core/tools/design.md` 与 `domains/core/mcp/design.md`。

## 工作区隔离

| 要求 | 量化约束 |
|------|---------|
| 文件工具边界 | read/write/edit 经 Go `os.Root`(Linux 上 openat2 `RESOLVE_BENEATH`,其他平台模拟),TOCTOU 安全 |
| 搜索工具边界 | glob/grep 在 spawn 子进程前校验目录参数并拒绝 symlink 逃逸 |
| bash 路径边界 | 检测 `cd` / 绝对路径 / `..` / 命令替换逃逸;硬阻断 `/proc`、`/sys`、`/dev` |
| 默认 allow-list | `[BashWorkingDir, os.TempDir()]`;经 `tools.allowed_dirs` YAML 扩展 |
| 一致性 | 安全包络一次性写入工具构造选项,所有代理共享同一约束,不随代理选择改变 |

## 命令风险分级

bash 子命令分四档:**Safe / Caution / Dangerous / Blocked**。

| 档位 | 处理 |
|------|------|
| Blocked | 在 BashTool 内部 **硬拒绝**(不可绕过) |
| Dangerous | HTTP 模式拒绝;CLI 模式确认提示 |
| Caution / Safe | 放行 |

## 间接提示注入防御(工具结果注入扫描)

| 要求 | 量化约束 |
|------|---------|
| 扫描点 | 每个工具返回,在结果消息追加到模型上下文 **之前**(Run 与 RunStream 两条路径) |
| 规则集 | 20 条默认规则(role hijack、Unicode tag、bidi override、ChatML/Llama 标记、prompt extraction、exfil command+URL、markdown-image exfil、boundary break),每条带 Low/Medium/High 严重度 |
| 动作 | `log`(记录放行)/ `rewrite`(quarantine-wrap)/ `block`(返回错误工具结果) |
| 硬升级 | `block_on_severity=high`:High 严重度结构性攻击无条件升为 block,不论配置动作 |
| 默认姿态 | `enabled=true, action=log, block_on_severity=high` |

## MCP 凭据过滤

| 要求 | 量化约束 |
|------|---------|
| 扫描点 | 4 个:client 出站参数、client 入站结果、server 入站参数、server 出站结果 |
| 规则集 | AWS/GitHub/Slack/JWT/PEM/Stripe/Google/OpenAI/Bearer + 关键词门控的 aws_secret_key / generic_api_key;5 条 JSON 字段名规则 |
| 动作 | `log` / `redact`(`[REDACTED:<type>]` 保留 JSON 结构)/ `block` |
| 扫描上限 | `MaxScanBytes` 默认 256 KiB(262144),超限截断并置 `Truncated` |
| 明文保护 | 事件载荷只带掩码预览(前 4 字符 + `****`),绝不带明文 |
| 默认姿态 | `enabled=true, action=redact, max_scan_bytes=262144` |
| 误报控制 | 默认 allowlist 放行 UUID v4 与 40-hex git SHA |

## 凭据与密钥管理

- API key 经配置 / 环境变量注入,**绝不** 硬编码;只存在于出站 HTTP 请求,不进入 envelope、trace JSONL、任何日志行。
- web_search API key 仅在出站请求中;provider 错误 envelope 不含 key。

## MCP 网络暴露

| 要求 | 量化约束 |
|------|---------|
| 默认绑定 | Streamable HTTP 默认 `127.0.0.1:7801` |
| 非 loopback 拒绝 | 任何非 loopback 绑定(含裸 `:port`)在未设 `mcp.server.auth_token` 时 **启动期拒绝** |
| 认证 | 常量时间 Bearer token 比较 |
| DNS rebinding 防护 | 继承 MCP Go SDK 默认(loopback socket 上非 loopback Host 头返回 403) |

## 安全的负空间(Non-Goals)

- **不** 做用户认证 / 授权 / 多租户隔离(MCP HTTP 仅单个共享 Bearer token)。
- **不** 对 trace JSONL 载荷做独立 PII / 密钥脱敏 —— 上游 guard + credscrub 擦边界,但直写意味着穿过代理的 PII/密钥仍落盘;缓解:保持 `trace.enabled=false` 直到 P3-5 导出管线引入脱敏。
- **不** 做记忆加密。
- **不** 暴露原始工具(bash/read/write/edit/glob/grep)为顶层 MCP 工具。
