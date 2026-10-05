package setup

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/dispatches"
)

func TestNew_PrimaryCarriesUseSkill(t *testing.T) {
	cfg := &configs.Config{
		LLM:         configs.LLMConfig{Model: "test-model"},
		Agents:      configs.AgentsConfig{MaxIterations: 10},
		Tools:       configs.ToolsConfig{BashTimeout: 10},
		Orchestrate: configs.OrchestrateConfig{Mode: configs.OrchestrateModeUnified},
	}

	result, err := New(cfg, &mockChatCompleter{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	primary, ok := result.Dispatcher.Primary().(*taskagent.Agent)
	if !ok {
		t.Fatalf("Primary is %T, want *taskagent.Agent", result.Dispatcher.Primary())
	}

	names := toolNameSet(t, primary)
	if !names[dispatches.PrimaryToolUseSkill] {
		t.Fatal("Primary missing use_skill")
	}

	coder, ok := result.Agent("coder").(*taskagent.Agent)
	if !ok {
		t.Fatal("coder is not *taskagent.Agent")
	}
	if toolNameSet(t, coder)[dispatches.PrimaryToolUseSkill] {
		t.Error("coder must not carry use_skill")
	}
}

func TestNew_SkillDirMergesIntoSpawnWorkerEnum(t *testing.T) {
	dir := t.TempDir()
	writeSetupSkill(t, dir, "security-audit", "Security review", "Hunt for auth bugs.", "")
	writeSetupSkill(t, dir, "release-notes", "Changelog voice", "Write notes.", "")
	writeSetupSkill(t, dir, "Not-Valid", "bad", "nope", "")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := &configs.Config{
		LLM:         configs.LLMConfig{Model: "test-model"},
		Agents:      configs.AgentsConfig{MaxIterations: 10, SkillDir: dir},
		Tools:       configs.ToolsConfig{BashTimeout: 10},
		Orchestrate: configs.OrchestrateConfig{Mode: configs.OrchestrateModeUnified},
	}

	result, err := New(cfg, &mockChatCompleter{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New with invalid skill must still succeed: %v", err)
	}

	opts := result.Dispatcher.WorkerOptions()
	ids := make([]string, 0, len(opts.Skills))
	for _, s := range opts.Skills {
		ids = append(ids, s.ID)
	}

	if !containsStr(ids, "security-audit") || !containsStr(ids, "release-notes") {
		t.Errorf("spawn_worker skills = %v, want file skills", ids)
	}
	if containsStr(ids, "Not-Valid") {
		t.Errorf("invalid skill leaked: %v", ids)
	}
	if !strings.Contains(buf.String(), "skip file skill") {
		t.Errorf("expected skip Warn for invalid skill, got %q", buf.String())
	}
}

func TestNew_UseSkillNextTurnInjectsInstructionsWithoutChangingTools(t *testing.T) {
	cc := &scriptedCompleter{responses: []*largemodel.Response{
		largemodel.FakeToolCallResponse(schema.ProtocolOpenAIChat, []schema.ToolCall{{
			ID:        "call-skill",
			Name:      dispatches.PrimaryToolUseSkill,
			Arguments: `{"skill":"review"}`,
		}}, schema.Usage{}),
		largemodel.FakeStopResponse(schema.ProtocolOpenAIChat, "activated", schema.Usage{}),
		largemodel.FakeStopResponse(schema.ProtocolOpenAIChat, "continued", schema.Usage{}),
	}}

	var mu sync.Mutex
	var events []schema.Event
	hm := hook.NewManager()
	hm.Register(hook.NewHookFunc(func(_ context.Context, e schema.Event) error {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		return nil
	}))

	cfg := &configs.Config{
		LLM:         configs.LLMConfig{Model: "test-model"},
		Agents:      configs.AgentsConfig{MaxIterations: 10},
		Tools:       configs.ToolsConfig{BashTimeout: 10},
		Orchestrate: configs.OrchestrateConfig{Mode: configs.OrchestrateModeUnified},
	}

	result, err := New(cfg, cc, nil, nil, &Options{HookManager: hm})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	primary := result.Dispatcher.Primary()
	sessionID := "sess-skill"

	if _, err := primary.Run(context.Background(), &schema.RunRequest{
		SessionID: sessionID,
		Messages:  []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "load review")},
	}); err != nil {
		t.Fatalf("run 1: %v", err)
	}

	if prompt := cc.systemPrompt(0); strings.Contains(prompt, `<skill name="review">`) {
		t.Fatal("first run should not contain activated skill instructions")
	}

	mu.Lock()
	foundActivate := false
	for _, e := range events {
		if e.Type == schema.EventSkillActivate && e.SessionID == sessionID {
			foundActivate = true
			break
		}
	}
	mu.Unlock()
	if !foundActivate {
		t.Fatal("EventSkillActivate missing or SessionID empty")
	}

	if _, err := primary.Run(context.Background(), &schema.RunRequest{
		SessionID: sessionID,
		Messages:  []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "continue")},
	}); err != nil {
		t.Fatalf("run 2: %v", err)
	}

	if prompt := cc.systemPrompt(2); !strings.Contains(prompt, `<skill name="review">`) {
		t.Errorf("second run missing skill block, prompt:\n%s", prompt)
	}

	before := cc.toolNames(0)
	after := cc.toolNames(2)
	if !slices.Equal(before, after) {
		t.Errorf("LLM tool set changed after activation: before=%v after=%v", before, after)
	}
}

func containsStr(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func writeSetupSkill(t *testing.T, root, name, description, body, extra string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n" + extra + "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type scriptedCompleter struct {
	mu        sync.Mutex
	reqs      []*largemodel.Request
	responses []*largemodel.Response
	idx       int
}

func (c *scriptedCompleter) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }

func (c *scriptedCompleter) Call(_ context.Context, req *largemodel.Request) (*largemodel.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *req
	c.reqs = append(c.reqs, &cp)
	if c.idx >= len(c.responses) {
		return largemodel.FakeStopResponse(schema.ProtocolOpenAIChat, "ok", schema.Usage{}), nil
	}
	resp := c.responses[c.idx]
	c.idx++
	return resp, nil
}

func (c *scriptedCompleter) CallStream(_ context.Context, _ *largemodel.Request) (*largemodel.Stream, error) {
	return nil, nil
}

func (c *scriptedCompleter) systemPrompt(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.reqs) {
		return ""
	}
	for _, msg := range c.reqs[i].Messages {
		if msg.Role() == schema.RoleSystem {
			return msg.Text()
		}
	}
	return ""
}

func (c *scriptedCompleter) toolNames(i int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.reqs) {
		return nil
	}
	out := make([]string, 0, len(c.reqs[i].Tools))
	for _, d := range c.reqs[i].Tools {
		out = append(out, d.Name)
	}
	return out
}
