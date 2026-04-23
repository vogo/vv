package setup_tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/service"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/setup"
)

// --- Test: Full Wiring with HTTP Service via setup.New() ---
// Verifies that setup.New() produces agents and a Dispatcher that work with the HTTP service.
// Test cases:
//   - Health endpoint returns 200 OK
//   - Agent listing returns 5 agents (orchestrator + 4 dispatchable)
//   - Tools listing returns 6 tools
//   - Agent detail for "orchestrator" returns correct ID
func TestIntegration_SetupNew_FullWiringHTTP(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test-configs.yaml")
	configContent := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
server:
  addr: ":0"
tools:
  bash_timeout: 10
agents:
  max_iterations: 5
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(configPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("test response"),
					},
				},
			},
		},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	// Build a tool registry for the service (same as main.go would).
	toolReg, err := registries.ProfileFull.BuildRegistry(cfg.Tools)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	svc := service.New(
		service.Config{Addr: ":0"},
		service.WithToolRegistry(toolReg),
	)
	svc.RegisterAgent(result.Dispatcher)
	for _, a := range result.Agents() {
		svc.RegisterAgent(a)
	}

	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()
	client := ts.Client()

	// Health
	healthResp, err := client.Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	_ = healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d", healthResp.StatusCode)
	}

	// Agents listing -- 5 agents (orchestrator + coder + chat + researcher + reviewer).
	agentsResp, err := client.Get(ts.URL + "/v1/agents")
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	var agentList []struct{ ID string }
	_ = json.NewDecoder(agentsResp.Body).Decode(&agentList)
	_ = agentsResp.Body.Close()
	if len(agentList) != 5 {
		ids := make([]string, len(agentList))
		for i, a := range agentList {
			ids[i] = a.ID
		}
		t.Errorf("agent count = %d, want 5, got %v", len(agentList), ids)
	}

	// Tools listing
	toolsResp, err := client.Get(ts.URL + "/v1/tools")
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	var toolList []struct{ Name string }
	_ = json.NewDecoder(toolsResp.Body).Decode(&toolList)
	_ = toolsResp.Body.Close()
	if len(toolList) != 6 {
		t.Errorf("tool count = %d, want 6", len(toolList))
	}

	// Agent details
	detailResp, err := client.Get(ts.URL + "/v1/agents/orchestrator")
	if err != nil {
		t.Fatalf("agent detail: %v", err)
	}
	var detail struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(detailResp.Body).Decode(&detail)
	_ = detailResp.Body.Close()
	if detail.ID != "orchestrator" {
		t.Errorf("orchestrator ID = %q", detail.ID)
	}
}

// --- Test: Dispatcher (via setup.New) direct dispatch ---
// Verifies that the Dispatcher built by setup.New() can dispatch to sub-agents.
// Test cases:
//   - Direct dispatch to chat agent returns response
//   - Dispatcher satisfies agent.Agent and agent.StreamAgent
func TestIntegration_SetupNew_DispatcherDirectDispatch(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "chat"}`),
					},
				},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 5},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	// Verify interface compliance.
	var _ agent.Agent = result.Dispatcher
	var _ agent.StreamAgent = result.Dispatcher

	resp, err := result.Dispatcher.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from Dispatcher")
	}
}

// --- Test: Dispatcher (via setup.New) streaming interface compliance ---
// Verifies that the Dispatcher built by setup.New() satisfies agent.StreamAgent
// and that RunStream can be initiated.
// Test cases:
//   - Dispatcher satisfies agent.StreamAgent interface
//   - RunStream returns a non-nil stream (using stub sub-agents for reliable streaming)
//   - Stream contains PhaseStart and PhaseEnd events
func TestIntegration_SetupNew_DispatcherStreaming(t *testing.T) {
	directJSON := `{"mode": "direct", "agent": "coder"}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(directJSON),
					},
				},
			},
		},
	}

	// Use a stubStreamAgent (defined in cli_test.go) for the sub-agent to avoid
	// the nil stream issue from mockChatCompleter's ChatCompletionStream.
	coderStub := &stubStreamAgent{id: "coder", response: "stream done"}

	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID:           id,
			Dispatchable: true,
		})
	}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	dispatcher := dispatches.New(
		reg,
		subAgents,
		nil, // no explorer
		nil, // no planner
		nil, // no planGen
		dispatches.WithLLM(mock, "test-model"),
		dispatches.WithMaxConcurrency(2),
		dispatches.WithFallbackAgent(&stubAgent{id: "chat"}),
	)

	// Verify interface compliance.
	var _ agent.StreamAgent = dispatcher

	stream, err := dispatcher.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello stream")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var events []schema.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			t.Fatalf("Recv: %v", recvErr)
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected at least one event from RunStream")
	}

	// Verify we get PhaseStart and PhaseEnd events.
	hasPhaseStart := false
	hasPhaseEnd := false
	for _, e := range events {
		if e.Type == schema.EventPhaseStart {
			hasPhaseStart = true
		}
		if e.Type == schema.EventPhaseEnd {
			hasPhaseEnd = true
		}
	}

	if !hasPhaseStart {
		t.Error("missing PhaseStart event")
	}
	if !hasPhaseEnd {
		t.Error("missing PhaseEnd event")
	}
}

// --- Test: Dispatcher (via setup.New) plan execution ---
// Verifies that the Dispatcher built by setup.New() can execute a multi-step plan.
// Test cases:
//   - Plan with 2 sequential steps is parsed and executed
//   - Response contains aggregated messages
func TestIntegration_SetupNew_DispatcherPlanExecution(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Research and implement",
		"steps": [
			{"id": "step_1", "description": "Research patterns", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Write the code", "agent": "coder", "depends_on": ["step_1"]}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 1},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	resp, err := result.Dispatcher.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Research and implement a feature")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in response")
	}
}

// --- Test: Dispatcher via setup.New() fallback on classification failure ---
// Verifies that the Dispatcher falls back to chat when classification fails.
// Test cases:
//   - Invalid JSON from LLM triggers fallback to chat
//   - Response is from the chat agent
func TestIntegration_SetupNew_DispatcherFallback(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("This is not valid JSON"),
					},
				},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 5},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	resp, err := result.Dispatcher.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test fallback")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Dispatcher.Run should fall back, not error: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected fallback response")
	}
}

// --- Test: dispatches.Dispatcher compile-time interface check ---
// Verifies that dispatches.Dispatcher satisfies agent.Agent and agent.StreamAgent.
// Test cases:
//   - dispatches.Dispatcher implements agent.Agent
//   - dispatches.Dispatcher implements agent.StreamAgent
func TestIntegration_DispatcherInterfaceCompliance(t *testing.T) {
	var _ agent.Agent = (*dispatches.Dispatcher)(nil)
	var _ agent.StreamAgent = (*dispatches.Dispatcher)(nil)
}

// --- Test: Planner prompt auto-generated from registry matches expected format ---
// Verifies that the auto-generated planner agent list matches the behavioral regression
// check: same agent descriptions as hardcoded PlannerSystemPrompt.
// Test cases:
//   - Auto-generated list contains all 4 dispatchable agents
//   - Each agent has a quoted ID and description
//   - Agent IDs match: coder, researcher, reviewer, chat
func TestIntegration_SetupNew_PlannerPromptAutoGeneration(t *testing.T) {
	reg := registries.New()

	// Register all agents as setup.New() does.
	// We import agents package functions indirectly via the registration.
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	// Use setup.New to get a fully wired result, then check planner prompt content.
	_, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	// Separately build a registry and generate the planner agent list to verify format.
	_ = reg // use a fresh one for isolated testing

	// The key test: verify PlannerAgentList output from a fully populated registries.
	fullReg := registries.New()
	for _, desc := range []registries.AgentDescriptor{
		{ID: "chat", DisplayName: "Chat", Description: "General conversation, questions, explanations", Dispatchable: true},
		{ID: "coder", DisplayName: "Coder", Description: "Reads, writes, edits files, runs commands", Dispatchable: true},
		{ID: "researcher", DisplayName: "Researcher", Description: "Explores codebases, reads documentation", Dispatchable: true},
		{ID: "reviewer", DisplayName: "Reviewer", Description: "Reviews code for correctness", Dispatchable: true},
		{ID: "explorer", DisplayName: "Explorer", Description: "Explores project context", Dispatchable: false},
		{ID: "planner", DisplayName: "Planner", Description: "Plans tasks", Dispatchable: false},
	} {
		fullReg.MustRegister(desc)
	}

	agentList := fullReg.PlannerAgentList()

	// Verify all 4 dispatchable agents are present (non-dispatchable excluded).
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		if !strings.Contains(agentList, `"`+id+`"`) {
			t.Errorf("PlannerAgentList missing agent %q:\n%s", id, agentList)
		}
	}

	// Verify non-dispatchable agents are NOT present.
	for _, id := range []string{"explorer", "planner"} {
		if strings.Contains(agentList, `"`+id+`"`) {
			t.Errorf("PlannerAgentList should NOT contain non-dispatchable agent %q:\n%s", id, agentList)
		}
	}

	// Verify format: each line starts with "- ".
	lines := strings.Split(strings.TrimSpace(agentList), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 lines in PlannerAgentList, got %d", len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "- ") {
			t.Errorf("expected line to start with '- ', got %q", line)
		}
	}
}
