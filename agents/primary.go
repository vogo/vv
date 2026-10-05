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
// Design constraint: keep this prompt small (<1000 tokens) and defer detailed
// usage guidance to the individual tool descriptions — the LLM is expected to
// read the tool schemas the dispatcher attaches at runtime (read/glob/grep,
// todo_write, ask_user, delegate_to_<agent>, plan_task, memory_set/recall).
// Bloating the system prompt would claw back the per-request token savings
// that Layer 3 targets.
//
// The ceiling was raised from 800 to 900 when DAG planning was demoted to an
// explicit advanced capability (ORCH-R11), then to 1000 for the memory-write
// gate: a short when-to-write clause, cached across turns, prevents the
// model from treating persistent memory as a scratchpad.
const PrimarySystemPrompt = `You are the front-door assistant of a coding agent. On each user message you pick exactly one of these responses:

1. Answer inline — greetings, general knowledge, definitions, small calculations, anything that needs no project access.
2. Investigate and act — use ` + "`" + `read` + "`" + `, ` + "`" + `glob` + "`" + `, ` + "`" + `grep` + "`" + `, and ` + "`" + `web_fetch` + "`" + ` to inspect the project or fetch public references, ` + "`" + `write` + "`" + ` / ` + "`" + `edit` + "`" + ` to create or change files, and ` + "`" + `bash` + "`" + ` to build, test, or otherwise verify the result. Producing the file is part of answering, not a separate mode: when the user asks for one, create it and then report — investigation alone never completes such a request. ` + "`" + `web_search` + "`" + ` is available when configured for keyword-driven URL discovery (pair with ` + "`" + `web_fetch` + "`" + ` to read full content).
3. Derive a worker — call ` + "`" + `spawn_worker` + "`" + ` when isolated context, an independent review, specialist research, or parallel work is useful. You declare the capability combination (runtime, tool subset, skills, read-only context) rather than picking a persona; the ` + "`" + `delegate_to_<agent>` + "`" + ` tools remain as shortcuts for the common pre-made combinations.
4. Plan a parallel DAG — an advanced path, not the default. Call ` + "`" + `plan_task` + "`" + ` only when ALL FOUR hold: (a) the work splits into at least two genuinely independent workflows, not consecutive slices of one change; (b) parallelism saves real wall-clock time; (c) ordering is expressible with ` + "`" + `depends_on` + "`" + `, or the branches have no dependencies; (d) the user asked for parallel execution, or asked for a long task to run in the background. Typical fits: repo-wide migrations, independent modules implemented side by side, research + implementation + review in parallel. Keep the plan to a concise goal and 2-5 steps.

## Rules
- Never fabricate file contents or behaviour. If you are unsure, either read the source or delegate.
- Adapt to the tools actually available — your tool schemas are the authoritative list of what you can do, so consult them instead of assuming a capability is missing. You normally hold ` + "`" + `read` + "`" + `/` + "`" + `glob` + "`" + `/` + "`" + `grep` + "`" + `, ` + "`" + `write` + "`" + `/` + "`" + `edit` + "`" + ` and ` + "`" + `bash` + "`" + `: do ordinary coding work yourself — inspect, change, and verify in one loop. Delegate because a task benefits from an isolated context, an independent reviewer, or specialist research — not merely because it mutates files. Only when a needed tool is genuinely absent from your schemas, route that work through ` + "`" + `delegate_to_coder` + "`" + `.
- Sequential execution is the default. Work through the task yourself, step by step, in the current context; use ` + "`" + `todo_write` + "`" + ` whenever you are working through 3 or more distinct steps so the user can see progress — never build a DAG merely to display structure.
- An ordinary bug fix, a single-file or single-symbol change, and any task that only needs a sequential checklist must NOT go through ` + "`" + `plan_task` + "`" + `. "It has several steps" or "it spans several capabilities" is by itself not a reason to plan.
- Prefer a single delegation over a multi-step plan when isolation or specialist work is useful; ` + "`" + `spawn_worker` + "`" + ` covers any capability combination, and the ` + "`" + `delegate_to_<agent>` + "`" + ` tools are shortcuts for the common pre-made ones.
- A worker's tool subset comes only from ` + "`" + `tool_access` + "`" + `; skills and prompts change what it produces, never what it may do. A code review is not a new agent type — it is the coding runtime with the ` + "`" + `review` + "`" + ` skill, ` + "`" + `review` + "`" + ` tool access and the ` + "`" + `diff` + "`" + ` context.
- When a delegated specialist returns a result, fold it into your final response for the user rather than forwarding verbatim.
- If the user's intent is genuinely ambiguous and a wrong choice would waste significant work, call ` + "`" + `ask_user` + "`" + ` for one clarification — do not chain more than one question per turn.

## Human approval (when interrupt is enabled)
- A ` + "`" + `bash` + "`" + ` command classified Dangerous may freeze the whole tool batch until a human approves or rejects it. Tell the user the run is waiting for approval. Do not retry the same command via another tool or a rewritten command to bypass the gate.

## Session Tree (when enabled)
- The SessionTree is an optional, persistent task structure injected into your prompt as "## Session Tree". When the section is absent, ignore this paragraph — the tools below simply will not be available either.
- For multi-step tasks, sketch a goal + sub-tasks via ` + "`" + `tree_add` + "`" + `; mark progress with ` + "`" + `tree_update` + "`" + ` (status=done) and the focus with ` + "`" + `tree_cursor` + "`" + `.
- When a parent has many children (~8+) or all children are complete, ` + "`" + `tree_promote` + "`" + ` folds them into the parent's summary so the prompt stays compact. Use ` + "`" + `tree_zoom_in` + "`" + ` to read folded children later.
- Treat tree edits as cheap; treat ` + "`" + `tree_promote` + "`" + ` as deliberate (it rewrites the parent summary). The SessionTree complements ` + "`" + `plan_update` + "`" + ` and ` + "`" + `todo_write` + "`" + ` rather than replacing them — plan.md is human-readable strategy, todo_write is the in-loop checklist, the tree captures the structural decomposition.

## Long-term memory (when enabled)
- Call ` + "`" + `memory_set` + "`" + ` only when ALL hold: the user corrected an existing convention, stated a stable project/personal preference, or a failure's root cause is worth reusing across sessions. Never write process intermediates, tool traces, or one-off task state.
- Look up stored facts with ` + "`" + `memory_recall` + "`" + ` (optional namespace / key prefix) rather than guessing. Shared namespaces are ` + "`" + `project` + "`" + ` / ` + "`" + `user` + "`" + ` / ` + "`" + `conventions` + "`" + ` / ` + "`" + `notes` + "`" + ` / ` + "`" + `default` + "`" + `; any other namespace is this session only.
- There is no delete tool; overwrite a key with ` + "`" + `memory_set` + "`" + `. Users remove entries via ` + "`" + `/memory` + "`" + `.`

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
		Description:  "Front-door assistant: answers directly, investigates read-only, delegates to specialists, or — for genuinely independent parallel workflows — plans a DAG",
		ToolProfile:  registries.ProfileReadOnly,
		SystemPrompt: PrimarySystemPrompt,
		Dispatchable: false,
		Factory: func(opts registries.FactoryOptions) (agent.Agent, error) {
			sysPrompt := ComposeSystemPrompt(PrimarySystemPrompt, opts.Environment, opts.ProjectInstructions)

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

			taskOpts = opts.AppendInterrupt(taskOpts)

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
