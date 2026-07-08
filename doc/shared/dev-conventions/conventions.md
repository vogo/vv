# 开发约定(跨领域)

源出 `vv/CLAUDE.md`,在 spec 层固化为跨领域约定。`shared/` 约定优先于领域内同名约定。

## 代码组织

- 模块 `vv` 基于 `vage` 框架与 `aimodel` SDK;依赖经本地 `replace` 指向 `../aimodel` 与 `../vage`,兄弟模块改动立即生效。
- 单元测试与源码同目录,无外部依赖。
- 集成测试在 `integrations/<group>_tests/<scenario>_tests/`,依赖 `VV_LLM_API_KEY`(或 `AI_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`)。
- 测试结束清理构建产物。

## 工程惯例

| 约定 | 内容 |
|------|------|
| 函数式选项 | 工具/代理配置统一用 functional options 模式 |
| context 贯穿 | 一切跨函数边界操作经 `context.Context` 传递(递归深度、session id、user-path 等都走 context) |
| 协议无关 | LLM 流量一律经 `aimodel`,不直接调 provider 原生 SDK |
| 零成本默认 | 可选子系统未启用即不构造 |
| 构建可移植 | 保持 `CGO_ENABLED=0` 可构建 |

## 构建与测试

```bash
make build   # format → lint → test
make test    # go test ./... with coverage
make lint    # golangci-lint run
```

## 配置约定

- 配置优先级:CLI 标志 > 环境变量 > 配置文件(`~/.vv/vv.yaml`)> 默认值。
- 环境变量前缀 `VV_`(如 `VV_TRACE_ENABLED`、`VV_MEMORY_BACKEND`、`VV_PERMISSION_MODE`)。
- 子系统开关默认值:见 `architecture/architecture.md` § 零成本默认与各领域 design。

## 文档约定

- 文档中文为主,技术术语保留英文。
- spec 不含函数/字段/源码行号引用;对照代码读 `vv/CLAUDE.md` 的快速索引。
- 变更记录在 `vv-changes/<year>/<month>/<day>/<id>-<slug>/`,含 requirement / design / code-review / test-report。
