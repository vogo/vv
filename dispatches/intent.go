package dispatches

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/debugs"
	"github.com/vogo/vv/registries"
)

// Deprecated: as of M6 the unified Primary Assistant is the sole production
// dispatch path; the classical intent → execute → summarize pipeline this
// file implements is retained only as a fallback that fires when the
// dispatcher is constructed without a Primary (legacy callers + a handful
// of regression tests). The remaining helpers are minimal — explorer /
// reassess / classify / planner-driven intent recognition were removed in
// M6 G2 because the explorer and chat sub-agents are gone. Plan to delete
// this file outright in M7+ once the classical fallback in dispatch.go is
// retired.

// intentSystemPromptTemplate is the template for the intent recognition prompt.
const intentSystemPromptTemplate = `You are an intelligent task router. Analyze the user's request and determine how to handle it.

## Available Agents
{{.AgentList}}

## Instructions
Analyze the request and respond with ONLY a JSON object. No other text.

### For simple tasks (single agent):
{"mode": "direct", "agent": "<agent_id>"}

### For complex tasks (multi-step):
{"mode": "plan", "plan": {"goal": "...", "steps": [{"id": "step_1", "description": "...", "agent": "coder", "depends_on": []}]}}

## Rules
1. Use "direct" mode for tasks that clearly map to one agent capability.
2. Use "plan" mode only when the task genuinely requires multiple distinct steps across different capabilities.
3. For plan steps, use "depends_on" to specify ordering. Steps without dependencies run in parallel.
4. Keep plans focused: typically 2-5 steps.
5. Default to "coder" for ambiguous coding tasks.
6. When project context is provided, reference specific files, functions, or patterns in step descriptions.
7. For specialized sub-tasks, add an optional "dynamic_spec" to a plan step:
   {"id": "step_1", "description": "...", "agent": "coder", "depends_on": [],
    "dynamic_spec": {"base_type": "coder", "system_prompt": "You are a Go testing specialist...", "tool_access": "full"}}
   Fields: "base_type" (required, same as "agent"), "system_prompt" (optional), "tool_access" (optional: "full"/"read-only"/"none"), "model" (optional).
   Only use dynamic_spec when a sub-task needs a specialized prompt or different tool access. For most tasks, omit it.`

// BuildIntentSystemPrompt constructs the intent recognition prompt from the registry.
func BuildIntentSystemPrompt(reg *registries.Registry) string {
	return strings.Replace(intentSystemPromptTemplate, "{{.AgentList}}", reg.PlannerAgentList(), 1)
}

// recognizeIntent performs intent recognition on the classical fallback
// path. Since M6 only the unified-intent (M2) and direct-LLM branches
// remain — the planner-agent and explorer-driven branches were retired
// alongside the explorer and chat sub-agents.
func (d *Dispatcher) recognizeIntent(ctx context.Context, req *schema.RunRequest) (*IntentResult, string, *aimodel.Usage, error) {
	depth := DepthFrom(ctx)
	if depth >= d.maxRecursionDepth {
		return d.fallbackIntent(), "", nil, nil
	}

	if d.llm == nil {
		return d.fallbackIntent(), "", nil, nil
	}

	if d.useUnifiedIntent() {
		return d.recognizeIntentUnified(ctx, req)
	}

	return d.recognizeIntentDirect(ctx, req)
}

// recognizeIntentStream is the streaming variant of recognizeIntent.
func (d *Dispatcher) recognizeIntentStream(ctx context.Context, req *schema.RunRequest, _ func(schema.Event) error) (*IntentResult, string, *aimodel.Usage, error) {
	depth := DepthFrom(ctx)
	if depth >= d.maxRecursionDepth {
		return d.fallbackIntent(), "", nil, nil
	}

	if d.llm == nil {
		return d.fallbackIntent(), "", nil, nil
	}

	if d.useUnifiedIntent() {
		return d.recognizeIntentUnified(ctx, req)
	}

	return d.recognizeIntentDirect(ctx, req)
}

// useUnifiedIntent reports whether the tool-calling unified-intent pathway
// should take the next recognizeIntent call. A routing LLM (the dedicated
// router client if configured via M3, otherwise the main LLM) is required
// because the unified path is inherently an LLM call.
func (d *Dispatcher) useUnifiedIntent() bool {
	return d.unifiedIntent && d.routerClient() != nil
}

// recognizeIntentDirect makes a direct LLM call for intent recognition.
// The M5 NeedsExploration branch was removed in M6 because the explorer
// sub-agent no longer exists.
func (d *Dispatcher) recognizeIntentDirect(ctx context.Context, req *schema.RunRequest) (*IntentResult, string, *aimodel.Usage, error) {
	ctx = debugs.WithAgentName(ctx, "intent")
	systemPrompt := d.intentSystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a task planner. Respond with JSON: {\"mode\": \"direct\", \"agent\": \"" + d.fallbackAgentName() + "\"}"
	}

	systemPrompt = appendProjectInstructions(systemPrompt, d.projectInstructions)
	systemPrompt = strings.Replace(systemPrompt, "{{.WorkingDir}}", d.workingDir, 1)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(systemPrompt),
	})
	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:    d.routerModelName(),
		Messages: msgs,
	}

	resp, err := d.routerClient().ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, "", nil, err
	}

	usage := &resp.Usage

	if len(resp.Choices) == 0 {
		return nil, "", usage, fmt.Errorf("empty intent recognition response")
	}

	text := resp.Choices[0].Message.Content.Text()
	jsonStr := extractJSON(text)

	var intent IntentResult
	if err := json.Unmarshal([]byte(jsonStr), &intent); err != nil {
		return nil, "", usage, fmt.Errorf("parse intent JSON: %w", err)
	}

	// NeedsExploration is honoured no longer (explorer removed in M6 G2).
	// If the LLM still emits the flag, normalise to a direct fallback so
	// the call site does not branch on a now-meaningless field.
	if intent.NeedsExploration {
		intent.NeedsExploration = false
		intent.Mode = "direct"
		intent.Agent = d.fallbackAgentName()
	}

	if err := intent.validate(d.registry, d.subAgents); err != nil {
		return nil, "", usage, err
	}

	return &intent, "", usage, nil
}

// fallbackIntent returns a default intent routing to the fallback agent.
func (d *Dispatcher) fallbackIntent() *IntentResult {
	return &IntentResult{
		Mode:  "direct",
		Agent: d.fallbackAgentName(),
	}
}

// buildIntentSummary builds a human-readable summary of the intent result.
func buildIntentSummary(intent *IntentResult) string {
	if intent == nil {
		return ""
	}

	if intent.Mode == IntentModeAnswered {
		return unifiedAnsweredSummary
	}

	if intent.Mode == "direct" {
		return fmt.Sprintf("Direct -> %s", intent.Agent)
	}

	if intent.Plan == nil || len(intent.Plan.Steps) == 0 {
		return ""
	}

	var sb strings.Builder

	sb.WriteString(intent.Plan.Goal)

	for i, step := range intent.Plan.Steps {
		fmt.Fprintf(&sb, "\n  %d. [%s] %s", i+1, step.Agent, step.Description)
	}

	return sb.String()
}
