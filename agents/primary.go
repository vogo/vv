package agents

import (
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vv/registries"
)

// PrimaryAgentID is the registry identifier for the Primary Assistant. Kept
// as a constant so setup code, dispatcher wiring, and tests reference the
// same key.
const PrimaryAgentID = "primary"

// PrimarySystemPrompt is the system prompt for the Primary Assistant — the
// front-door agent that replaces the classical intent/execute/summarize
// pipeline when `orchestrate.mode: unified` is enabled.
//
// Design constraint: keep this prompt small (<800 tokens) and defer detailed
// usage guidance to the individual tool descriptions — the LLM is expected to
// read the tool schemas the dispatcher attaches at runtime (read/glob/grep,
// todo_write, ask_user, delegate_to_<agent>, plan_task). Bloating the system
// prompt would claw back the per-request token savings that Layer 3 targets.
const PrimarySystemPrompt = `You are the front-door assistant of a coding agent. On each user message you pick exactly one of these responses:

1. Answer inline — greetings, general knowledge, definitions, small calculations, anything that needs no project access.
2. Read-only investigation — use the ` + "`" + `read` + "`" + `, ` + "`" + `web_fetch` + "`" + `, ` + "`" + `glob` + "`" + `, and ` + "`" + `grep` + "`" + ` tools to inspect the project or fetch public references, then answer. ` + "`" + `web_search` + "`" + ` is available when configured for keyword-driven URL discovery (pair with ` + "`" + `web_fetch` + "`" + ` to read full content).
3. Derive a worker — call ` + "`" + `spawn_worker` + "`" + ` when isolated context, an independent review, specialist research, or parallel work is useful. You declare the capability combination (runtime, tool subset, skills, read-only context) rather than picking a persona; the ` + "`" + `delegate_to_<agent>` + "`" + ` tools remain as shortcuts for the common pre-made combinations.
4. Plan a DAG — when the task genuinely spans multiple capability combinations, call ` + "`" + `plan_task` + "`" + ` with a concise goal and 2-5 steps. Use ` + "`" + `depends_on` + "`" + ` for ordering; steps without dependencies run in parallel.

## Rules
- Never fabricate file contents or behaviour. If you are unsure, either read the source or delegate.
- Adapt to the tools actually available. When write/edit/bash tools are present, perform ordinary coding work directly: inspect, change, and verify in one loop. When they are absent, route mutations through ` + "`" + `delegate_to_coder` + "`" + `.
- Prefer direct execution for work that shares the current context. Prefer a single derived worker over a multi-step plan when isolation or a distinct capability combination is useful.
- A worker's tool subset comes only from ` + "`" + `tool_access` + "`" + `; skills and prompts change what it produces, never what it may do. A code review is not a new agent type — it is the coding runtime with the ` + "`" + `review` + "`" + ` skill, ` + "`" + `review` + "`" + ` tool access and the ` + "`" + `diff` + "`" + ` context.
- Use ` + "`" + `todo_write` + "`" + ` whenever you are working through 3 or more distinct steps so the user can see progress.
- When a delegated specialist returns a result, fold it into your final response for the user rather than forwarding verbatim.
- If the user's intent is genuinely ambiguous and a wrong choice would waste significant work, call ` + "`" + `ask_user` + "`" + ` for one clarification — do not chain more than one question per turn.

## Session Tree (when enabled)
- The SessionTree is an optional, persistent task structure injected into your prompt as "## Session Tree". When the section is absent, ignore this paragraph — the tools below simply will not be available either.
- For multi-step tasks, sketch a goal + sub-tasks via ` + "`" + `tree_add` + "`" + `; mark progress with ` + "`" + `tree_update` + "`" + ` (status=done) and the focus with ` + "`" + `tree_cursor` + "`" + `.
- When a parent has many children (~8+) or all children are complete, ` + "`" + `tree_promote` + "`" + ` folds them into the parent's summary so the prompt stays compact. Use ` + "`" + `tree_zoom_in` + "`" + ` to read folded children later.
- Treat tree edits as cheap; treat ` + "`" + `tree_promote` + "`" + ` as deliberate (it rewrites the parent summary). The SessionTree complements ` + "`" + `plan_update` + "`" + ` and ` + "`" + `todo_write` + "`" + ` rather than replacing them — plan.md is human-readable strategy, todo_write is the in-loop checklist, the tree captures the structural decomposition.`

// RegisterPrimary registers the Primary Assistant descriptor with reg. The
// descriptor is marked non-dispatchable so HTTP sub-agent exposure does not
// advertise it automatically — the Primary is invoked only by the dispatcher
// when unified mode is active.
//
// The tool set advertised to this agent is assembled externally (in
// setup.go): ProfileReadOnly capabilities plus todo_write, ask_user, the
// per-specialist delegate_to_* tools, and plan_task. The Factory simply
// wires whatever ToolRegistry the caller attaches.
func RegisterPrimary(reg *registries.Registry) {
	reg.MustRegister(registries.AgentDescriptor{
		ID:           PrimaryAgentID,
		DisplayName:  "Primary Assistant",
		Description:  "Front-door assistant: answers directly, investigates read-only, delegates to specialists, or plans a multi-step DAG",
		ToolProfile:  registries.ProfileReadOnly,
		SystemPrompt: PrimarySystemPrompt,
		Dispatchable: false,
		Factory: func(opts registries.FactoryOptions) (agent.Agent, error) {
			sysPrompt := AppendProjectInstructions(PrimarySystemPrompt, opts.ProjectInstructions)

			taskOpts := []taskagent.Option{
				taskagent.WithCaller(opts.LLM),
				taskagent.WithModel(opts.Model),
				taskagent.WithSystemPrompt(prompt.StringPrompt(sysPrompt)),
				taskagent.WithMaxIterations(opts.MaxIterations),
				taskagent.WithRunTokenBudget(opts.RunTokenBudget),
				taskagent.WithMaxParallelToolCalls(opts.MaxParallelToolCalls),
				taskagent.WithPromptCaching(opts.PromptCaching),
			}

			if opts.ToolRegistry != nil {
				taskOpts = append(taskOpts, taskagent.WithToolRegistry(opts.ToolRegistry))
			}

			if opts.Memory != nil {
				taskOpts = append(taskOpts, taskagent.WithMemory(opts.Memory))
			}

			if len(opts.ToolResultGuards) > 0 {
				taskOpts = append(taskOpts, taskagent.WithToolResultGuards(opts.ToolResultGuards...))
			}

			if opts.HookManager != nil {
				taskOpts = append(taskOpts, taskagent.WithHookManager(opts.HookManager))
			}

			if len(opts.ExtraContextSources) > 0 {
				taskOpts = append(taskOpts, taskagent.WithExtraSources(opts.ExtraContextSources...))
			}

			if opts.IterationStore != nil {
				taskOpts = append(taskOpts, taskagent.WithIterationStore(opts.IterationStore))
			}

			if opts.BuildReportSink != nil {
				taskOpts = append(taskOpts, taskagent.WithBuildReportSink(opts.BuildReportSink))
			}

			if opts.CheckpointFailureCB != nil {
				taskOpts = append(taskOpts, taskagent.WithCheckpointFailureCallback(opts.CheckpointFailureCB))
			}

			return taskagent.New(
				agent.Config{
					ID:          PrimaryAgentID,
					Name:        "Primary Assistant",
					Description: "Unified front-door assistant: answers, investigates, delegates, or plans",
				},
				taskOpts...,
			), nil
		},
	})
}
