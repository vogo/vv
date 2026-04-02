package dispatches

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

// intentSystemPromptTemplate is the template for the intent recognition prompt.
const intentSystemPromptTemplate = `You are an intelligent task router. Analyze the user's request and determine how to handle it.

## Available Agents
{{.AgentList}}

## Instructions
Analyze the request and respond with ONLY a JSON object. No other text.

If you need to explore the project first (e.g., the request references specific files, code, or project structure you don't know about), respond:
{"needs_exploration": true}

If you can classify the request directly:

### For simple tasks (single agent):
{"needs_exploration": false, "mode": "direct", "agent": "<agent_id>"}

### For complex tasks (multi-step):
{"needs_exploration": false, "mode": "plan", "plan": {"goal": "...", "steps": [{"id": "step_1", "description": "...", "agent": "coder", "depends_on": []}]}}

## Rules
1. Use "direct" mode for tasks that clearly map to one agent capability.
2. Use "plan" mode only when the task genuinely requires multiple distinct steps across different capabilities.
3. Set "needs_exploration" to true only when the request references project-specific details you cannot assess without exploration.
4. For greetings, general questions, or clearly simple tasks, never request exploration.
5. For plan steps, use "depends_on" to specify ordering. Steps without dependencies run in parallel.
6. Keep plans focused: typically 2-5 steps.
7. Default to "coder" for ambiguous coding tasks, "chat" for general questions.
8. When project context is provided, reference specific files, functions, or patterns in step descriptions.
9. For specialized sub-tasks, add an optional "dynamic_spec" to a plan step:
   {"id": "step_1", "description": "...", "agent": "coder", "depends_on": [],
    "dynamic_spec": {"base_type": "coder", "system_prompt": "You are a Go testing specialist...", "tool_access": "full"}}
   Fields: "base_type" (required, same as "agent"), "system_prompt" (optional), "tool_access" (optional: "full"/"read-only"/"none"), "model" (optional).
   Only use dynamic_spec when a sub-task needs a specialized prompt or different tool access. For most tasks, omit it.`

// BuildIntentSystemPrompt constructs the intent recognition prompt from the registry.
func BuildIntentSystemPrompt(reg *registries.Registry) string {
	return strings.Replace(intentSystemPromptTemplate, "{{.AgentList}}", reg.PlannerAgentList(), 1)
}

// recognizeIntent performs intent recognition, optionally invoking the explorer.
// This replaces the separate explore() + classify() calls.
// Returns the intent result, context summary from exploration (empty if none),
// token usage, and error.
func (d *Dispatcher) recognizeIntent(ctx context.Context, req *schema.RunRequest) (*IntentResult, string, *aimodel.Usage, error) {
	depth := DepthFrom(ctx)

	// At max depth, skip intent recognition.
	if depth >= d.maxRecursionDepth {
		return d.fallbackIntent(), "", nil, nil
	}

	// If no planner agent and no LLM, return fallback.
	if d.plannerAgent == nil && d.llm == nil {
		return d.fallbackIntent(), "", nil, nil
	}

	// If planner agent is set, use it (preserves existing behavior for complex scenarios).
	if d.plannerAgent != nil {
		return d.recognizeIntentViaPlanner(ctx, req)
	}

	// Direct LLM call for intent recognition.
	return d.recognizeIntentDirect(ctx, req)
}

// recognizeIntentStream is the streaming variant of recognizeIntent.
func (d *Dispatcher) recognizeIntentStream(ctx context.Context, req *schema.RunRequest, send func(schema.Event) error) (*IntentResult, string, *aimodel.Usage, error) {
	depth := DepthFrom(ctx)

	if depth >= d.maxRecursionDepth {
		return d.fallbackIntent(), "", nil, nil
	}

	if d.plannerAgent == nil && d.llm == nil {
		return d.fallbackIntent(), "", nil, nil
	}

	if d.plannerAgent != nil {
		return d.recognizeIntentViaPlannerStream(ctx, req, send)
	}

	return d.recognizeIntentDirect(ctx, req)
}

// recognizeIntentDirect makes a direct LLM call for intent recognition.
func (d *Dispatcher) recognizeIntentDirect(ctx context.Context, req *schema.RunRequest) (*IntentResult, string, *aimodel.Usage, error) {
	systemPrompt := d.intentSystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a task planner. Respond with JSON: {\"needs_exploration\": false, \"mode\": \"direct\", \"agent\": \"chat\"}"
	}

	// Append project instructions to intent recognition prompt.
	systemPrompt = appendProjectInstructions(systemPrompt, d.projectInstructions)

	systemPrompt = strings.Replace(systemPrompt, "{{.WorkingDir}}", d.workingDir, 1)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(systemPrompt),
	})
	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:    d.model,
		Messages: msgs,
	}

	resp, err := d.llm.ChatCompletion(ctx, chatReq)
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

	// If needs exploration, invoke explorer then re-assess.
	if intent.NeedsExploration && d.explorerAgent != nil {
		contextSummary, exploreUsage := d.explore(ctx, req)
		totalUsage := aggregateUsage(usage, exploreUsage)

		// Re-assess with exploration context.
		reIntent, reassessUsage, reassessErr := d.reassessIntent(ctx, req, contextSummary)
		totalUsage = aggregateUsage(totalUsage, reassessUsage)

		if reassessErr != nil {
			return nil, contextSummary, totalUsage, reassessErr
		}

		return reIntent, contextSummary, totalUsage, nil
	}

	// If needs exploration but no explorer, just proceed with what we have.
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

// reassessIntent makes a second LLM call after exploration to re-classify.
func (d *Dispatcher) reassessIntent(ctx context.Context, req *schema.RunRequest, contextSummary string) (*IntentResult, *aimodel.Usage, error) {
	systemPrompt := d.intentSystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a task planner. Respond with JSON: {\"needs_exploration\": false, \"mode\": \"direct\", \"agent\": \"chat\"}"
	}

	// Append project instructions to re-assessment prompt.
	systemPrompt = appendProjectInstructions(systemPrompt, d.projectInstructions)

	systemPrompt = strings.Replace(systemPrompt, "{{.WorkingDir}}", d.workingDir, 1)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+3)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(systemPrompt),
	})

	if contextSummary != "" {
		msgs = append(msgs, aimodel.Message{
			Role:    aimodel.RoleUser,
			Content: aimodel.NewTextContent(fmt.Sprintf("Project context from exploration:\n%s", contextSummary)),
		})
	}

	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:    d.model,
		Messages: msgs,
	}

	resp, err := d.llm.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, nil, err
	}

	usage := &resp.Usage

	if len(resp.Choices) == 0 {
		return nil, usage, fmt.Errorf("empty re-assessment response")
	}

	text := resp.Choices[0].Message.Content.Text()
	jsonStr := extractJSON(text)

	var intent IntentResult
	if err := json.Unmarshal([]byte(jsonStr), &intent); err != nil {
		return nil, usage, fmt.Errorf("parse re-assessment JSON: %w", err)
	}

	// Force needs_exploration to false after re-assessment.
	intent.NeedsExploration = false

	if err := intent.validate(d.registry, d.subAgents); err != nil {
		return nil, usage, err
	}

	return &intent, usage, nil
}

// recognizeIntentViaPlanner uses the planner agent for classification
// (preserves existing behavior when planner agent is configured).
func (d *Dispatcher) recognizeIntentViaPlanner(ctx context.Context, req *schema.RunRequest) (*IntentResult, string, *aimodel.Usage, error) {
	// First, optionally explore.
	contextSummary, exploreUsage := d.explore(ctx, req)

	// Then classify via planner.
	cr, planUsage, err := d.classify(ctx, req, contextSummary)
	if err != nil {
		return nil, contextSummary, aggregateUsage(exploreUsage, planUsage), err
	}

	// Convert ClassifyResult to IntentResult.
	intent := &IntentResult{
		Mode:  cr.Mode,
		Agent: cr.Agent,
		Plan:  cr.Plan,
	}

	return intent, contextSummary, aggregateUsage(exploreUsage, planUsage), nil
}

// recognizeIntentViaPlannerStream is the streaming variant using the planner agent.
func (d *Dispatcher) recognizeIntentViaPlannerStream(ctx context.Context, req *schema.RunRequest, send func(schema.Event) error) (*IntentResult, string, *aimodel.Usage, error) {
	agentID := d.ID()
	sessionID := req.SessionID

	// Explore (streaming).
	var contextSummary string
	var exploreUsage *aimodel.Usage

	if d.explorerAgent != nil {
		if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
			Phase: "explore",
		})); err != nil {
			return nil, "", nil, err
		}

		var exploreTracker phaseTracker
		contextSummary, exploreUsage = d.exploreStream(ctx, req, exploreTracker.wrap(send))

		if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
			Phase:            "explore",
			ToolCalls:        exploreTracker.toolCalls,
			PromptTokens:     exploreTracker.promptTokens,
			CompletionTokens: exploreTracker.completionTokens,
		})); err != nil {
			return nil, contextSummary, exploreUsage, err
		}
	}

	// Classify via planner (streaming).
	cr, planUsage, err := d.classifyStream(ctx, req, contextSummary, send)
	if err != nil {
		return nil, contextSummary, aggregateUsage(exploreUsage, planUsage), err
	}

	intent := &IntentResult{
		Mode:  cr.Mode,
		Agent: cr.Agent,
		Plan:  cr.Plan,
	}

	return intent, contextSummary, aggregateUsage(exploreUsage, planUsage), nil
}

// fallbackIntent returns a default intent routing to the fallback agent.
func (d *Dispatcher) fallbackIntent() *IntentResult {
	return &IntentResult{
		Mode:  "direct",
		Agent: d.fallbackAgentName(),
	}
}

// fallbackAgentName returns the name of the fallback agent.
func (d *Dispatcher) fallbackAgentName() string {
	if d.fallbackAgent != nil {
		return d.fallbackAgent.ID()
	}

	// Try to find any available agent.
	for id := range d.subAgents {
		return id
	}

	return "chat"
}

// buildIntentSummary builds a human-readable summary of the intent result.
func buildIntentSummary(intent *IntentResult) string {
	if intent == nil {
		return ""
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

// explore calls the explorer sub-agent to build project context.
// Returns the context summary text and usage.
func (d *Dispatcher) explore(ctx context.Context, req *schema.RunRequest) (string, *aimodel.Usage) {
	if d.explorerAgent == nil {
		return "", nil
	}

	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	msgs = append(msgs, req.Messages...)

	explorerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	resp, err := d.explorerAgent.Run(ctx, explorerReq)
	if err != nil {
		slog.Warn("orchestrator: explorer failed", "error", err)

		return "", nil
	}

	if len(resp.Messages) == 0 {
		return "", resp.Usage
	}

	return resp.Messages[0].Content.Text(), resp.Usage
}

// exploreStream calls the explorer sub-agent using streaming, forwarding events via send.
func (d *Dispatcher) exploreStream(
	ctx context.Context,
	req *schema.RunRequest,
	send func(schema.Event) error,
) (string, *aimodel.Usage) {
	if d.explorerAgent == nil {
		return "", nil
	}

	sa, ok := d.explorerAgent.(agent.StreamAgent)
	if !ok {
		return d.explore(ctx, req)
	}

	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	msgs = append(msgs, req.Messages...)

	explorerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	stream, err := sa.RunStream(ctx, explorerReq)
	if err != nil {
		slog.Warn("orchestrator: explorer stream failed", "error", err)

		return "", nil
	}

	defer func() { _ = stream.Close() }()

	var textBuf strings.Builder

	var usage aimodel.Usage

	var hasUsage bool

	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if isEOF(recvErr) {
				break
			}

			slog.Warn("orchestrator: explorer stream recv error", "error", recvErr)

			break
		}

		switch event.Type {
		case schema.EventTextDelta:
			if data, ok := event.Data.(schema.TextDeltaData); ok {
				textBuf.WriteString(data.Delta)
			}
		case schema.EventToolCallStart, schema.EventToolResult, schema.EventError:
			if err := send(event); err != nil {
				slog.Warn("orchestrator: explorer stream send error", "error", err)
			}
		case schema.EventLLMCallEnd:
			if data, ok := event.Data.(schema.LLMCallEndData); ok {
				hasUsage = true
				usage.PromptTokens += data.PromptTokens
				usage.CompletionTokens += data.CompletionTokens
				usage.TotalTokens += data.TotalTokens
			}

			if err := send(event); err != nil {
				slog.Warn("orchestrator: explorer stream send error", "error", err)
			}
		case schema.EventAgentEnd:
			if data, ok := event.Data.(schema.AgentEndData); ok {
				if textBuf.Len() == 0 && data.Message != "" {
					textBuf.WriteString(data.Message)
				}
			}
		}
	}

	if hasUsage {
		return textBuf.String(), &usage
	}

	return textBuf.String(), nil
}

// classify calls the planner sub-agent to classify/plan the request.
func (d *Dispatcher) classify(ctx context.Context, req *schema.RunRequest, contextSummary string) (*ClassifyResult, *aimodel.Usage, error) {
	if d.plannerAgent == nil {
		return d.classifyDirect(ctx, req)
	}

	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	if contextSummary != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Project context:\n%s", contextSummary),
		))
	}

	msgs = append(msgs, req.Messages...)

	plannerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	resp, err := d.plannerAgent.Run(ctx, plannerReq)
	if err != nil {
		return nil, nil, fmt.Errorf("planner run: %w", err)
	}

	usage := resp.Usage

	if len(resp.Messages) == 0 {
		return nil, usage, fmt.Errorf("empty planner response")
	}

	text := resp.Messages[0].Content.Text()
	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usage, fmt.Errorf("parse planner JSON: %w", err)
	}

	if err := result.validate(d.registry, d.subAgents); err != nil {
		return nil, usage, err
	}

	return &result, usage, nil
}

// classifyStream calls the planner using streaming.
func (d *Dispatcher) classifyStream(
	ctx context.Context,
	req *schema.RunRequest,
	contextSummary string,
	send func(schema.Event) error,
) (*ClassifyResult, *aimodel.Usage, error) {
	if d.plannerAgent == nil {
		return d.classifyDirect(ctx, req)
	}

	sa, ok := d.plannerAgent.(agent.StreamAgent)
	if !ok {
		return d.classify(ctx, req, contextSummary)
	}

	var msgs []schema.Message

	if d.workingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", d.workingDir),
		))
	}

	if contextSummary != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Project context:\n%s", contextSummary),
		))
	}

	msgs = append(msgs, req.Messages...)

	plannerReq := &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	}

	stream, err := sa.RunStream(ctx, plannerReq)
	if err != nil {
		return nil, nil, fmt.Errorf("planner stream: %w", err)
	}

	defer func() { _ = stream.Close() }()

	var textBuf strings.Builder

	var planUsage aimodel.Usage

	var hasUsage bool

	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if isEOF(recvErr) {
				break
			}

			if hasUsage {
				return nil, &planUsage, fmt.Errorf("planner stream recv: %w", recvErr)
			}

			return nil, nil, fmt.Errorf("planner stream recv: %w", recvErr)
		}

		switch event.Type {
		case schema.EventTextDelta:
			if data, ok := event.Data.(schema.TextDeltaData); ok {
				textBuf.WriteString(data.Delta)
			}
		case schema.EventToolCallStart, schema.EventToolResult, schema.EventError:
			if err := send(event); err != nil {
				slog.Warn("orchestrator: planner stream send error", "error", err)
			}
		case schema.EventLLMCallEnd:
			if data, ok := event.Data.(schema.LLMCallEndData); ok {
				hasUsage = true
				planUsage.PromptTokens += data.PromptTokens
				planUsage.CompletionTokens += data.CompletionTokens
				planUsage.TotalTokens += data.TotalTokens
			}

			if err := send(event); err != nil {
				slog.Warn("orchestrator: planner stream send error", "error", err)
			}
		}
	}

	var usagePtr *aimodel.Usage
	if hasUsage {
		usagePtr = &planUsage
	}

	text := textBuf.String()
	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usagePtr, fmt.Errorf("parse planner JSON: %w", err)
	}

	if err := result.validate(d.registry, d.subAgents); err != nil {
		return nil, usagePtr, err
	}

	return &result, usagePtr, nil
}

// classifyDirect makes a direct LLM call to classify the task (fallback when no planner agent).
func (d *Dispatcher) classifyDirect(ctx context.Context, req *schema.RunRequest) (*ClassifyResult, *aimodel.Usage, error) {
	systemPrompt := d.intentSystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a task planner. Respond with JSON: {\"mode\": \"direct\", \"agent\": \"chat\"}"
	}

	// Append project instructions to classification prompt.
	systemPrompt = appendProjectInstructions(systemPrompt, d.projectInstructions)

	systemPrompt = strings.Replace(systemPrompt, "{{.WorkingDir}}", d.workingDir, 1)

	msgs := make([]aimodel.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, aimodel.Message{
		Role:    aimodel.RoleSystem,
		Content: aimodel.NewTextContent(systemPrompt),
	})
	msgs = append(msgs, schema.ToAIModelMessages(req.Messages)...)

	chatReq := &aimodel.ChatRequest{
		Model:    d.model,
		Messages: msgs,
	}

	resp, err := d.llm.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, nil, err
	}

	usage := &resp.Usage

	if len(resp.Choices) == 0 {
		return nil, usage, fmt.Errorf("empty classification response")
	}

	text := resp.Choices[0].Message.Content.Text()
	jsonStr := extractJSON(text)

	var result ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, usage, fmt.Errorf("parse classification JSON: %w", err)
	}

	if err := result.validate(d.registry, d.subAgents); err != nil {
		return nil, usage, err
	}

	return &result, usage, nil
}
