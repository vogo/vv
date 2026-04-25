# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`vv` is a Go CLI/HTTP agent application built on the `vage` framework and `aimodel` SDK. As of M7 the dispatch pipeline is **unified-only**: every request flows through a single Primary Assistant that owns its own tool-driven decision loop (`answer_directly` / `delegate_to_<agent>` / `plan_task`). Specialist sub-agents (coder, researcher, reviewer) are reached only by the Primary delegating to them via tool calls; there is no separate intent recognition / execute / summarize pipeline anymore (those phases were retired in M7). This is one module in a multi-module monorepo; see the parent `CLAUDE.md` for the full picture.

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

The `Dispatcher` implements `agent.StreamAgent`. As of M7 it is a thin forwarder:

1. **`Run` / `RunStream`** — depth-exceed fallback (degraded Primary, no tools) → otherwise → `runPrimary` / `runPrimaryStream` which forwards the whole request to `d.primaryAssistant`. Nil Primary returns an error; production code wires it through `setup.New`.
2. **Plan execution** (`dag.go` + `primary_tools.go`) — exposed to the Primary as the `plan_task` tool; the Primary chooses to fan out a DAG when the task spans multiple capabilities. `Dispatcher.RunPlan` is the synchronous entry point used by `plan_task`'s tool handler.
3. **Streaming on the depth-exceed path** emits a static `summarize` phase pair (M7 G4) so HTTP / SSE consumers see the same event-flow shape as the main path with zero LLM calls.

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

YAML at `~/.vv/vv.yaml`. Key sections: `llm` (provider, model, api_key, base_url), `server` (addr), `tools` (bash_timeout, bash_working_dir), `agents` (max_iterations), `cli` (permission_mode, deprecated confirm_tools), `memory`, `orchestrate` (max_concurrency, max_recursion_depth, primary_allow_bash). Environment overrides: `VV_LLM_API_KEY`, `VV_LLM_BASE_URL`, `VV_LLM_MODEL`, `VV_LLM_PROVIDER`, `VV_MODE`, `VV_SERVER_ADDR`, `VV_PERMISSION_MODE`, `VV_PRIMARY_ALLOW_BASH`. CLI flag: `--permission-mode`.

Stale orchestrate.* keys retained as YAML fields for backwards compatibility but **silently ignored** as of M7: `mode`, `legacy_phase_events`, `summary_policy`, `replan`, `fast_path`, `unified_intent`. `orchestrate.mode=classical` and `VV_ORCHESTRATE_LEGACY_PHASE_EVENTS=*` log a `slog.Warn` at Load.

## Conventions

- Integration tests in `integrations/<group>_tests/<scenario>_tests/` sub-packages (e.g., `cli_tests/permission_tests/`, `httpapis_tests/http_tests/`, `traces_tests/budget_tests/`), unit tests colocated with source.
- Delete build binaries after tests.
- Functional options pattern for tool/agent configuration.
- All operations use `context.Context`.
