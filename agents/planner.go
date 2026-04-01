package agents

import (
	"strings"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vv/registries"
)

// PlannerSystemPromptTemplate is the system prompt template for the planner sub-agent.
// The {{.AgentList}} placeholder is replaced with the dynamically generated agent list
// from the registry during setup.
const PlannerSystemPromptTemplate = `You are a task planner. You analyze a user's request and decide how to route it to the most appropriate agent(s).

## Available Agents
{{.AgentList}}
## Input
You receive:
1. The user's request
2. Optionally, a project context summary (from prior exploration)

## Response Format
You MUST respond with ONLY a JSON object. No other text.

### For simple tasks (single agent):
{"mode": "direct", "agent": "<agent_id>"}

### For complex tasks (multi-step):
{"mode": "plan", "plan": {"goal": "...", "steps": [{"id": "step_1", "description": "...", "agent": "coder", "depends_on": []}]}}

## Rules
1. Use "direct" mode for tasks that clearly map to one agent capability.
2. Use "plan" mode only when the task genuinely requires multiple distinct steps across different capabilities.
3. For plan steps, use "depends_on" to specify ordering. Steps without dependencies run in parallel.
4. Keep plans focused: typically 2-5 steps.
5. Default to "coder" for ambiguous coding tasks, "chat" for general questions.
6. When project context is provided, reference specific files, functions, or patterns in step descriptions.
7. For specialized sub-tasks, add an optional "dynamic_spec" to a plan step:
   {"id": "step_1", "description": "...", "agent": "coder", "depends_on": [],
    "dynamic_spec": {"base_type": "coder", "system_prompt": "You are a Go testing specialist...", "tool_access": "full"}}
   Fields: "base_type" (required, same as "agent"), "system_prompt" (optional), "tool_access" (optional: "full"/"read-only"/"none"), "model" (optional).
   Only use dynamic_spec when a sub-task needs a specialized prompt or different tool access. For most tasks, omit it.`

// PlannerSystemPrompt is kept for backward compatibility in tests. It contains
// the hardcoded agent list matching the original implementation.
const PlannerSystemPrompt = `You are a task planner. You analyze a user's request and decide how to route it to the most appropriate agent(s).

## Available Agents
- "coder": Reads, writes, edits files, runs commands, searches codebases, debugs
- "researcher": Explores codebases, reads documentation, gathers information (read-only)
- "reviewer": Reviews code for correctness, style, performance, security
- "chat": General conversation, questions, explanations, brainstorming

## Input
You receive:
1. The user's request
2. Optionally, a project context summary (from prior exploration)

## Response Format
You MUST respond with ONLY a JSON object. No other text.

### For simple tasks (single agent):
{"mode": "direct", "agent": "<agent_id>"}

### For complex tasks (multi-step):
{"mode": "plan", "plan": {"goal": "...", "steps": [{"id": "step_1", "description": "...", "agent": "coder", "depends_on": []}]}}

## Rules
1. Use "direct" mode for tasks that clearly map to one agent capability.
2. Use "plan" mode only when the task genuinely requires multiple distinct steps across different capabilities.
3. For plan steps, use "depends_on" to specify ordering. Steps without dependencies run in parallel.
4. Keep plans focused: typically 2-5 steps.
5. Default to "coder" for ambiguous coding tasks, "chat" for general questions.
6. When project context is provided, reference specific files, functions, or patterns in step descriptions.
7. For specialized sub-tasks, add an optional "dynamic_spec" to a plan step:
   {"id": "step_1", "description": "...", "agent": "coder", "depends_on": [],
    "dynamic_spec": {"base_type": "coder", "system_prompt": "You are a Go testing specialist...", "tool_access": "full"}}
   Fields: "base_type" (required, same as "agent"), "system_prompt" (optional), "tool_access" (optional: "full"/"read-only"/"none"), "model" (optional).
   Only use dynamic_spec when a sub-task needs a specialized prompt or different tool access. For most tasks, omit it.`

// BuildPlannerSystemPrompt constructs the planner system prompt by replacing
// the {{.AgentList}} placeholder with the actual agent list from the registries.
func BuildPlannerSystemPrompt(reg *registries.Registry) string {
	return strings.Replace(PlannerSystemPromptTemplate, "{{.AgentList}}", reg.PlannerAgentList(), 1)
}

// RegisterPlanner registers the planner agent descriptor with the registries.
func RegisterPlanner(reg *registries.Registry) {
	reg.MustRegister(registries.AgentDescriptor{
		ID:           "planner",
		DisplayName:  "Planner",
		Description:  "Analyzes requests and produces task classification or execution plans",
		ToolProfile:  registries.ProfileNone,
		SystemPrompt: "", // set dynamically via BuildPlannerSystemPrompt
		Dispatchable: false,
		Factory: func(opts registries.FactoryOptions) (agent.Agent, error) {
			// The system prompt should be set by the caller via opts or
			// computed from the registries. We use the fallback PlannerSystemPrompt
			// as a sensible default.
			sysPrompt := PlannerSystemPrompt

			return taskagent.New(
				agent.Config{
					ID:          "planner",
					Name:        "Planner Agent",
					Description: "Analyzes requests and produces task classification or execution plans",
				},
				taskagent.WithChatCompleter(opts.LLM),
				taskagent.WithModel(opts.Model),
				taskagent.WithSystemPrompt(prompt.StringPrompt(sysPrompt)),
				taskagent.WithMaxIterations(1),
			), nil
		},
	})
}
