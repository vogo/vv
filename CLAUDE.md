# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`vv` is a Go CLI/HTTP agent application built on the `vage` framework and `aimodel` SDK. The dispatch pipeline is unified only: every request flows through a single Primary Assistant that owns its own tool-driven control loop (`answer_directly` / `delegate_to_<agent>` / `plan_task`). Specialist sub-agents (coder, researcher, reviewer) are reached only when the Primary delegates to them via tool calls; there is no separate intent recognition / execute / summarize pipeline anymore. This is one module in a multi-module monorepo; see the parent `CLAUDE.md` for the full picture.

## Build & Test

Run from this directory (`vv/`):

```bash
make build          # format → lint → test
make test           # go test ./... with coverage
make lint           # golangci-lint run
go test ./tools/ -run TestRegister_AllRegistered -v   # single test example
```

Integration tests in `integrations/` require `VV_LLM_API_KEY` (or `AI_API_KEY`/`OPENAI_API_KEY`/`ANTHROPIC_API_KEY`). Unit tests have no external dependencies.

Dependencies use local `replace` directives (`../aimodel`, `../vage`), so changes to sibling modules are picked up immediately.

## Architecture

### Startup Flow

`main.go` → `configs.Load()` → `setup.Init()` → CLI (`cli.New().Run()`) or HTTP (`httpapis.Serve()`). The `-p` flag runs a single prompt non-interactively via `cli.RunPrompt()`.

### Dispatch Pipeline (`dispatches/`)

The `Dispatcher` implements `agent.StreamAgent`. It is a thin forwarder:

1. **`Run` / `RunStream`** — when recursion depth is exceeded, it falls back to the degraded Primary (no tools); otherwise it calls `runPrimary` / `runPrimaryStream`, which forwards the whole request to `d.primaryAssistant`. Nil Primary returns an error; production code wires it through `setup.New`.
2. **Plan execution** (`dag.go` + `primary_tools.go`) — exposed to the Primary as the `plan_task` tool; the Primary chooses to fan out a DAG when the task spans multiple capabilities. `Dispatcher.RunPlan` is the synchronous entry point used by `plan_task`'s tool handler.
3. **Streaming on the depth-exceed path** emits a static `summarize` phase pair so HTTP / SSE consumers see the same event flow as the main path, with zero LLM calls.

Key files: `depth.go` (recursion depth control via context), `types.go` (`ClassifyResult`, `Plan`, `PlanStep`, `DynamicAgentSpec`, `PlanAggregator`, `SummaryPolicy`/`ReplanPolicy` retained as types but no longer wired into dispatch), `primary_tools.go` (`delegate_to_*` / `plan_task` tool registration).

### Agent Registry (`registries/`)

Agents are registered via `AgentDescriptor` containing: ID, display name, `ToolProfile`, system prompt, and a `Factory` function. `ToolProfile` is capability-based (`CapRead`, `CapWrite`, `CapExecute`, `CapSearch`) — the same factory produces different tool access levels depending on the profile.

Four agents registered in `agents/`: `coder` (full), `researcher` (read-only), `reviewer` (read+bash), `planner` (none, non-dispatchable). The Primary Assistant is built directly in `setup.New` (not registered like the dispatchable agents) — its tool registry is composed from `delegate_to_<agent>` (one per dispatchable specialist), `plan_task`, `read`/`glob`/`grep`, `todo_write`, optionally `bash` (`orchestrate.primary_allow_bash`), and `ask_user`.

### Tool Names

Tools are named: `bash`, `read`, `write`, `edit`, `glob`, `grep`. These are registered from `vage/tool/{bash,read,write,edit,glob,grep}` packages. Three registry factories in `tools/tools.go`: `Register` (all 6), `RegisterReadOnly` (read/glob/grep), `RegisterReviewTools` (bash/read/glob/grep).

### CLI (`cli/`)

Bubble Tea TUI with:
- `model` struct tracking session state (idle, processing, confirmation)
- Permission-based tool control via `WrapRegistryWithPermission()` + `PermissionState` with four modes (default, accept-edits, auto, plan) and three-option confirmation dialog (Allow/Allow Always/Deny)
- Markdown rendering via glamour; incremental streaming output
- `cli/prompt.go` handles non-interactive `-p` mode with streaming to stdout/stderr

### Configuration (`configs/`)

YAML at `~/.vv/vv.yaml`. Key sections: `llm` (provider, model, api_key, base_url), `server` (addr), `tools` (bash_timeout, bash_working_dir), `agents` (max_iterations), `cli` (permission_mode, deprecated confirm_tools), `memory`, `orchestrate` (max_concurrency, max_recursion_depth, primary_allow_bash), `session` (enabled, dir). Environment overrides: `VV_LLM_API_KEY`, `VV_LLM_BASE_URL`, `VV_LLM_MODEL`, `VV_LLM_PROVIDER`, `VV_MODE`, `VV_SERVER_ADDR`, `VV_PERMISSION_MODE`, `VV_PRIMARY_ALLOW_BASH`, `VV_SESSION_ENABLED`, `VV_SESSION_DIR`. CLI flags: `--permission-mode`, `--session`.

### Session Subsystem (`vage/session` integration)

Persistent sessions are wired by default — opt out with `session.enabled: false` (or `VV_SESSION_ENABLED=false`) when the persistence cost is unwanted.

- **Storage layout**: `~/.vv/sessions/<project_path_name>/<id>/{meta.json, events.jsonl, state.json}` where `<project_path_name>` is the absolute working directory with `/` and `\` mapped to `_`, alphanumerics kept verbatim, and every other rune mapped to `-`. Empty working dir falls back to `default`. Override the root via `session.dir` / `VV_SESSION_DIR`.
- **Wiring**: `setup.Init` constructs a `FileSessionStore`, registers `session.SessionHook` on the same `hook.Manager` that drives trace logging (the manager is now built whenever **either** `trace.enabled` or `session.enabled` is true), and exposes the store via `InitResult.SessionStore`.
- **CLI flags**: `vv --session <id>` resumes (id-only — message history is not replayed; that is P8 checkpoint scope), `vv --session list` prints the most recent 20 sessions and exits, `vv --session new` forces a fresh id.
- **HTTP**: `/v1/sessions`, `/v1/sessions/{id}`, `/v1/sessions/{id}/events`, `PATCH /v1/sessions/{id}`, `DELETE /v1/sessions/{id}` mounted only when `SessionStore != nil`.
- **Coexistence with trace**: trace logger and session hook subscribe to the same event bus. Both can be on, off, or one of each — the manager constructs only when at least one is on, so the disabled-everything path stays zero-cost.

Legacy `orchestrate.*` keys remain in the YAML schema for compatibility, but they are **silently ignored**: `mode`, `legacy_phase_events`, `summary_policy`, `replan`, `fast_path`, `unified_intent`. `orchestrate.mode=classical` and `VV_ORCHESTRATE_LEGACY_PHASE_EVENTS=*` log a `slog.Warn` at Load.

## Conventions

- Integration tests in `integrations/<group>_tests/<scenario>_tests/` sub-packages (e.g., `cli_tests/permission_tests/`, `httpapis_tests/http_tests/`, `traces_tests/budget_tests/`), unit tests colocated with source.
- Delete build binaries after tests.
- Functional options pattern for tool/agent configuration.
- All operations use `context.Context`.
