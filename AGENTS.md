# VV

`vv` is an agent application built on the `vage` framework and the `aimodel` SDK, offering three run modes: CLI / HTTP / MCP. Every request is routed through a unified **Primary Assistant**, which either answers directly, performs read-only investigation, delegates to a specialist, or plans a DAG.

## Docs-to-Source Map

> Design docs are maintained as a DDD specification knowledge base under **`doc/`**. See `doc/architecture/architecture.md` for the overall architecture and `doc/domains/core/core-overview.md` for the domain index.

Each topic maps to one doc domain and one or more source directories:

| Topic | Domain spec (`doc/domains/core/`) | Source directories |
|------|---------|---------|
| Overall architecture & design principles | `doc/architecture/architecture.md` | — |
| Entry point / assembly hub / configuration system | configuration/ | main.go, setup/, configs/ |
| Dispatcher & Primary / orchestration planning | orchestration/ | dispatches/ |
| Agent design / registry & capability tiers | agents/ | agents/, registries/ |
| Tool collection & safety guardrails | tools/ | tools/, registries/ |
| CLI mode | cli/ | cli/ |
| HTTP mode | http-api/ | httpapis/ |
| MCP mode | mcp/ | mcps/ |
| Memory system | memory/ | memories/ |
| Session / Plan Workspace / Session Tree | session/ | sessionlogs/ (source of truth for conversations), setup/ (assembly; the rest reuses vage subsystems) |
| Cost tracking / budget enforcement | cost-tracking/, budget/ | (LLM middleware, setup/) |
| Observability (trace / debug / hooks) | trace/ | traces/, debugs/, hooks/ |
| Evaluation subsystem | eval/ | eval/ |

## Build & Test

```bash
make build          # format → lint → test
make test           # go test ./... with coverage
make lint           # golangci-lint run
go test ./tools/ -run TestRegister_AllRegistered -v   # single-test example
```

Integration tests live in `integrations/` and require the `VV_LLM_API_KEY` environment variable (or `AI_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`). Unit tests have no external dependencies.

Production dependencies are pinned to released versions in `go.mod`; when working against a sibling repository, add a local `replace` temporarily — never commit that override.

## Engineering Conventions

- Integration tests live in `integrations/<group>_tests/<scenario>_tests/`; unit tests sit next to the source they cover.
- Clean up build artifacts once tests finish.
- Tool/agent configuration consistently uses the functional options pattern.
- Everything crossing a function boundary is passed through `context.Context`.
- Documentation under `doc/` is written in Chinese, with technical terms kept in English.
