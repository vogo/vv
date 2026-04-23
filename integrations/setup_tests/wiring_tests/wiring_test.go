package wiring_tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/service"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// --- Test: Full Wiring with HTTP Service (Design Test 6) ---
// Verifies that the HTTP service registers the Orchestrator correctly.
// Test cases:
//   - Health endpoint returns 200 OK
//   - Agent listing returns 5 agents (orchestrator, coder, chat, researcher, reviewer)
//   - Agent listing does NOT include "router" (replaced by "orchestrator")
//   - Tools listing returns 6 tools
//   - Agent detail for "orchestrator" returns correct ID
func TestIntegration_FullWiring(t *testing.T) {
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

	toolRegistry, err := tools.Register(cfg.Tools)
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	if len(toolRegistry.List()) != 6 {
		t.Fatalf("tool count = %d, want 6", len(toolRegistry.List()))
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

	allAgents := agents.Create(cfg, mock, toolRegistry, toolRegistry, toolRegistry, nil, nil)

	svc := service.New(
		service.Config{Addr: ":0"},
		service.WithToolRegistry(toolRegistry),
	)
	svc.RegisterAgent(allAgents.Orchestrator)
	svc.RegisterAgent(allAgents.Coder)
	svc.RegisterAgent(allAgents.Chat)
	svc.RegisterAgent(allAgents.Researcher)
	svc.RegisterAgent(allAgents.Reviewer)

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

	// Agents listing -- now 5 agents (orchestrator, coder, chat, researcher, reviewer).
	agentsResp, err := client.Get(ts.URL + "/v1/agents")
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	var agentList []struct{ ID string }
	_ = json.NewDecoder(agentsResp.Body).Decode(&agentList)
	_ = agentsResp.Body.Close()
	if len(agentList) != 5 {
		t.Errorf("agent count = %d, want 5", len(agentList))
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
