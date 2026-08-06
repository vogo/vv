# vv

`vv` is an AI agent application built on the **vage** framework and the **aimodel** SDK — a developer-facing coding assistant and general-purpose conversational AI. Every request flows through the same **Primary Assistant**, which decides how to respond: answer directly, explore and act (read/write files, run commands), delegate to a specialist agent (coder / researcher / reviewer), or trigger multi-step DAG planning when a task spans multiple capability domains.

It offers three ways to connect:

- **CLI** — interactive TUI (default), plus single-prompt and evaluation modes.
- **HTTP** — a long-running REST + SSE server (sync / streaming / async).
- **MCP** — exposed as an MCP server for LLM IDEs such as Claude Desktop, Cursor, Cline, and Goose.

## Quick Start

### Build

```bash
go build -o vv .
```

(`make build` in the repository runs format → lint → test first.)

### First-time setup

vv needs an LLM API key. On first run it asks interactively and writes `~/.vv/vv.yaml`:

```bash
./vv
```

You can also set values directly in the config file, or override them with environment variables:

```bash
export VV_LLM_API_KEY=sk-...        # LLM API key
export VV_LLM_BASE_URL=...          # LLM base URL (empty = official endpoint)
export VV_LLM_MODEL=...             # model name
export VV_LLM_PROVIDER=openai       # openai or anthropic
```

The standard `ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL` / `ANTHROPIC_MODEL` group is also supported (any non-empty value selects the anthropic provider). Environment variables always override YAML configuration.

## Usage

### CLI (default mode)

```bash
vv                                    # interactive TUI
vv -p "explain the main.go file"      # single prompt, exits after the answer
vv -p "fix the bug in auth.go" 2>/dev/null   # suppress diagnostic output
vv --permission-mode auto             # permission modes: default / accept-edits / auto / plan
```

Sessions:

```bash
vv --session list                    # list recent sessions
vv --session new                     # force a fresh session
vv --session <session-id>            # resume a session
vv --resume <session-id>             # continue an interrupted run from its latest checkpoint
vv --tree <session-id>               # print a session's SessionTree
```

### HTTP mode

```bash
vv --mode http                       # or VV_MODE=http; listens on :8080 by default
vv --mode http --addr 127.0.0.1:9000 # override the listen address
```

### MCP mode

```bash
vv --mode mcp                        # stdio transport by default, for LLM IDEs
```

### Evaluation

```bash
vv -eval dataset.jsonl               # run evaluation over a JSONL dataset
vv -eval dataset.jsonl -eval-out report.json
```

## Configuration

- Default config file: `~/.vv/vv.yaml` (use `-config` to point elsewhere).
- Environment variables (e.g. `VV_LLM_API_KEY`, `VV_SERVER_ADDR`, `VV_MODE`, `VV_DEBUG`) **override** the same-named fields in YAML.
- Other useful variables: `VV_TRACE_ENABLED` enables JSONL tracing, `VV_SESSION_DIR` sets the session directory.

## Documentation

Design docs are maintained as DDD specs under [`doc/`](doc/):

- Architecture overview: [doc/architecture/architecture.md](doc/architecture/architecture.md)
- Domain index: [doc/domains/core/core-overview.md](doc/domains/core/core-overview.md)
- Project positioning: [doc/project.md](doc/project.md)
- Development guide: [AGENTS.md](AGENTS.md)
