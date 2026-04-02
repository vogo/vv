# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`vv` is a Go CLI/HTTP agent application built on the `vage` framework and `aimodel` SDK. It provides pre-built AI agents (coder, researcher, reviewer, chat) with an adaptive decision loop: intent recognition → execution → optional summarization. This is one module in a multi-module monorepo; see the parent `CLAUDE.md` for the full picture.

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

The `Dispatcher` implements `agent.StreamAgent` and uses an adaptive decision loop:

1. **Intent Recognition** (`intent.go`) — Single LLM call classifies user intent; on-demand explorer invocation when `needs_exploration: true`; returns `IntentResult` with mode `"direct"` (single agent) or `"plan"` (multi-step DAG). Explorer and planner are on-demand capabilities, not fixed stages.
2. **Execution** (`execute.go`) — Direct mode runs one sub-agent; plan mode builds a DAG via `orchestrate.ExecuteDAG` with configurable concurrency. Supports replanning on step failure (`ReplanPolicy`) via topological layer execution.
3. **Summarization** (`summarize.go`) — Optional phase controlled by `SummaryPolicy` (auto/always/never). Auto mode: generates summary for HTTP, skips for CLI.

Key files: `depth.go` (recursion depth control via context), `types.go` (`IntentResult`, `ReplanPolicy`, `SummaryPolicy`, `Plan`, `PlanStep`).

### Agent Registry (`registries/`)

Agents are registered via `AgentDescriptor` containing: ID, display name, `ToolProfile`, system prompt, and a `Factory` function. `ToolProfile` is capability-based (`CapRead`, `CapWrite`, `CapExecute`, `CapSearch`) — the same factory produces different tool access levels depending on the profile.

Six agents registered in `agents/`: `coder` (full), `researcher` (read-only), `reviewer` (read+bash), `chat` (none), `explorer` (read-only, non-dispatchable), `planner` (none, non-dispatchable).

### Tool Names

Tools are named: `bash`, `read`, `write`, `edit`, `glob`, `grep`. These are registered from `vage/tool/{bash,read,write,edit,glob,grep}` packages. Three registry factories in `tools/tools.go`: `Register` (all 6), `RegisterReadOnly` (read/glob/grep), `RegisterReviewTools` (bash/read/glob/grep).

### CLI (`cli/`)

Bubble Tea TUI with:
- `model` struct tracking session state (idle, processing, confirmation)
- Tool confirmation via `WrapRegistry()` + `confirmForm` (huh.Form) for tools in `cli.confirm_tools`
- Markdown rendering via glamour; incremental streaming output
- `cli/prompt.go` handles non-interactive `-p` mode with streaming to stdout/stderr

### Configuration (`configs/`)

YAML at `~/.vv/vv.yaml`. Key sections: `llm` (provider, model, api_key, base_url), `server` (addr), `tools` (bash_timeout, bash_working_dir), `agents` (max_iterations), `cli` (confirm_tools list), `memory`, `orchestrate` (max_concurrency, max_recursion_depth, summary_policy, replan). Environment overrides: `VV_LLM_API_KEY`, `VV_LLM_BASE_URL`, `VV_LLM_MODEL`, `VV_LLM_PROVIDER`, `VV_MODE`, `VV_SERVER_ADDR`.

## Conventions

- Integration tests in `integrations/<area>_tests/` sub-packages (e.g., `agents_tests/`, `dispatches_tests/`, `cli_tests/`), unit tests colocated with source.
- Delete build binaries after tests.
- Functional options pattern for tool/agent configuration.
- All operations use `context.Context`.
