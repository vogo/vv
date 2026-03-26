package integrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/service"
	"github.com/vogo/vagents/vaga/config"
	"github.com/vogo/vagents/vaga/tools"
)

// setupTestServer creates an httptest.Server backed by a real service.Service
// with stub agents for integration testing. Returns the server (caller must close).
func setupTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	chatAgent := agent.NewCustomAgent(agent.Config{
		ID:          "chat",
		Name:        "Chat Agent",
		Description: "Handles general conversation",
	}, func(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
		userMsg := ""
		if len(req.Messages) > 0 {
			userMsg = req.Messages[0].Content.Text()
		}
		return &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("Echo: " + userMsg),
				}, "chat"),
			},
		}, nil
	})

	coderAgent := agent.NewCustomAgent(agent.Config{
		ID:          "coder",
		Name:        "Coder Agent",
		Description: "Handles coding tasks",
	}, func(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
		return &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("code response"),
				}, "coder"),
			},
		}, nil
	})

	routerAgent := agent.NewCustomAgent(agent.Config{
		ID:          "router",
		Name:        "Router Agent",
		Description: "Routes requests",
	}, func(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
		return chatAgent.Run(ctx, req)
	})

	svc := service.New(
		service.Config{Addr: ":0"},
		service.WithToolRegistry(reg),
	)
	svc.RegisterAgent(routerAgent)
	svc.RegisterAgent(coderAgent)
	svc.RegisterAgent(chatAgent)

	ts := httptest.NewServer(svc.Handler())
	t.Cleanup(ts.Close)

	return ts
}

func TestIntegration_HTTP_HealthEndpoint(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
}

func TestIntegration_HTTP_AgentListing(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/v1/agents")
	if err != nil {
		t.Fatalf("GET /v1/agents: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var agentList []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&agentList); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if len(agentList) != 3 {
		t.Fatalf("got %d agents, want 3", len(agentList))
	}

	expectedIDs := []string{"chat", "coder", "router"}
	for i, a := range agentList {
		if a.ID != expectedIDs[i] {
			t.Errorf("agent[%d].ID = %q, want %q", i, a.ID, expectedIDs[i])
		}
	}
}

func TestIntegration_HTTP_GetSingleAgent(t *testing.T) {
	ts := setupTestServer(t)
	client := ts.Client()

	resp, err := client.Get(ts.URL + "/v1/agents/chat")
	if err != nil {
		t.Fatalf("GET /v1/agents/chat: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var agentInfo struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&agentInfo); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if agentInfo.ID != "chat" {
		t.Errorf("agent ID = %q, want %q", agentInfo.ID, "chat")
	}
	if agentInfo.Name != "Chat Agent" {
		t.Errorf("agent Name = %q, want %q", agentInfo.Name, "Chat Agent")
	}

	resp2, err := client.Get(ts.URL + "/v1/agents/nonexistent")
	if err != nil {
		t.Fatalf("GET /v1/agents/nonexistent: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("non-existent agent status = %d, want %d", resp2.StatusCode, http.StatusNotFound)
	}
}

func TestIntegration_HTTP_ToolListing(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/v1/tools")
	if err != nil {
		t.Fatalf("GET /v1/tools: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var toolList []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&toolList); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if len(toolList) != 6 {
		t.Fatalf("got %d tools, want 6", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "file_read", "file_write", "file_edit", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("missing tool %q in /v1/tools response", name)
		}
	}
}

func TestIntegration_HTTP_SyncRun(t *testing.T) {
	ts := setupTestServer(t)
	client := ts.Client()

	reqBody, _ := json.Marshal(schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("Hello, world!")},
	})

	resp, err := client.Post(ts.URL+"/v1/agents/chat/run", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/agents/chat/run: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, string(body))
	}

	var runResp schema.RunResponse
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(runResp.Messages) == 0 {
		t.Fatal("expected at least one message in response")
	}

	text := runResp.Messages[0].Content.Text()
	if !strings.Contains(text, "Hello, world!") {
		t.Errorf("response text = %q, expected it to contain 'Hello, world!'", text)
	}
}

func TestIntegration_HTTP_SyncRunNotFound(t *testing.T) {
	ts := setupTestServer(t)
	client := ts.Client()

	reqBody, _ := json.Marshal(schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})

	resp, err := client.Post(ts.URL+"/v1/agents/nonexistent/run", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestIntegration_HTTP_Streaming(t *testing.T) {
	ts := setupTestServer(t)
	client := ts.Client()

	reqBody, _ := json.Marshal(schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("stream test")},
	})

	resp, err := client.Post(ts.URL+"/v1/agents/chat/stream", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/agents/chat/stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, string(body))
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	var events []string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			events = append(events, after)
		}
	}

	if len(events) == 0 {
		t.Fatal("expected at least one SSE event")
	}

	hasAgentStart := false
	hasAgentEnd := false
	for _, e := range events {
		if e == "agent_start" {
			hasAgentStart = true
		}
		if e == "agent_end" {
			hasAgentEnd = true
		}
	}

	if !hasAgentStart {
		t.Error("missing agent_start SSE event")
	}
	if !hasAgentEnd {
		t.Error("missing agent_end SSE event")
	}
}

func TestIntegration_HTTP_AsyncTaskLifecycle(t *testing.T) {
	ts := setupTestServer(t)
	client := ts.Client()

	reqBody, _ := json.Marshal(schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("async test")},
	})

	resp, err := client.Post(ts.URL+"/v1/agents/chat/async", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/agents/chat/async: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusAccepted, string(body))
	}

	var asyncResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&asyncResp); err != nil {
		t.Fatalf("decode async response: %v", err)
	}

	taskID := asyncResp["task_id"]
	if taskID == "" {
		t.Fatal("expected non-empty task_id")
	}

	var task struct {
		ID       string `json:"id"`
		AgentID  string `json:"agent_id"`
		Status   string `json:"status"`
		Response *struct {
			Messages []json.RawMessage `json:"messages"`
		} `json:"response"`
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		taskResp, err := client.Get(ts.URL + "/v1/tasks/" + taskID)
		if err != nil {
			t.Fatalf("GET /v1/tasks/%s: %v", taskID, err)
		}

		if err := json.NewDecoder(taskResp.Body).Decode(&task); err != nil {
			_ = taskResp.Body.Close()
			t.Fatalf("decode task: %v", err)
		}
		_ = taskResp.Body.Close()

		if task.Status == "completed" || task.Status == "failed" {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	if task.Status != "completed" {
		t.Fatalf("task status = %q, want 'completed'", task.Status)
	}

	if task.AgentID != "chat" {
		t.Errorf("task agent_id = %q, want %q", task.AgentID, "chat")
	}
}

func TestIntegration_HTTP_AsyncTaskCancel(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	slowAgent := agent.NewCustomAgent(agent.Config{
		ID:          "slow",
		Name:        "Slow Agent",
		Description: "Takes a long time",
	}, func(ctx context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
		select {
		case <-time.After(30 * time.Second):
			return &schema.RunResponse{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	svc := service.New(
		service.Config{Addr: ":0"},
		service.WithToolRegistry(reg),
	)
	svc.RegisterAgent(slowAgent)

	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()
	client := ts.Client()

	reqBody, _ := json.Marshal(schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("slow task")},
	})

	resp, err := client.Post(ts.URL+"/v1/agents/slow/async", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST async: %v", err)
	}

	var asyncResp map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&asyncResp)
	_ = resp.Body.Close()

	taskID := asyncResp["task_id"]

	time.Sleep(100 * time.Millisecond)

	cancelResp, err := client.Post(ts.URL+"/v1/tasks/"+taskID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	defer func() { _ = cancelResp.Body.Close() }()

	if cancelResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(cancelResp.Body)
		t.Fatalf("cancel status = %d, want %d, body: %s", cancelResp.StatusCode, http.StatusOK, string(body))
	}

	time.Sleep(50 * time.Millisecond)
	taskResp, err := client.Get(ts.URL + "/v1/tasks/" + taskID)
	if err != nil {
		t.Fatalf("GET task: %v", err)
	}
	defer func() { _ = taskResp.Body.Close() }()

	var task struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(taskResp.Body).Decode(&task)

	if task.Status != "cancelled" {
		t.Errorf("task status = %q, want 'cancelled'", task.Status)
	}
}

func TestIntegration_HTTP_TaskNotFound(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/v1/tasks/nonexistent-id")
	if err != nil {
		t.Fatalf("GET /v1/tasks/nonexistent: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestIntegration_HTTP_InvalidRequestBody(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := ts.Client().Post(ts.URL+"/v1/agents/chat/run", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
