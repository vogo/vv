package agents

import (
	"context"
	"strings"
	"testing"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vv/registries"
)

func TestAppendProjectInstructions_Empty(t *testing.T) {
	base := "You are a helpful assistant."
	got := AppendProjectInstructions(base, "")

	if got != base {
		t.Errorf("AppendProjectInstructions(base, \"\") = %q, want %q", got, base)
	}
}

func TestAppendProjectInstructions_NonEmpty(t *testing.T) {
	base := "You are a helpful assistant."
	instructions := "Always use Go 1.22."
	got := AppendProjectInstructions(base, instructions)

	if !strings.Contains(got, base) {
		t.Error("result should contain the base prompt")
	}

	if !strings.Contains(got, "# Project Instructions") {
		t.Error("result should contain the project instructions header")
	}

	if !strings.Contains(got, instructions) {
		t.Error("result should contain the instructions content")
	}

	if !strings.Contains(got, "IMPORTANT:") {
		t.Error("result should contain the IMPORTANT prefix")
	}
}

func TestAppendProjectInstructions_PreservesMarkdown(t *testing.T) {
	base := "Base prompt."
	instructions := "# Build\n\n```bash\nmake build\n```\n\n## Notes\n- item 1\n- item 2"
	got := AppendProjectInstructions(base, instructions)

	if !strings.Contains(got, "```bash\nmake build\n```") {
		t.Error("result should preserve code blocks")
	}

	if !strings.Contains(got, "## Notes") {
		t.Error("result should preserve markdown headings")
	}

	if !strings.Contains(got, "- item 1\n- item 2") {
		t.Error("result should preserve markdown lists")
	}
}

func TestFactory_WithProjectInstructions(t *testing.T) {
	instructions := "Always respond in Japanese."

	agents := []struct {
		name     string
		register func(*registries.Registry)
		id       string
	}{
		{"coder", func(r *registries.Registry) { RegisterCoder(r) }, "coder"},
		{"researcher", func(r *registries.Registry) { RegisterResearcher(r) }, "researcher"},
		{"reviewer", func(r *registries.Registry) { RegisterReviewer(r) }, "reviewer"},
		{"planner", func(r *registries.Registry) { RegisterPlanner(r) }, "planner"},
	}

	for _, tc := range agents {
		t.Run(tc.name, func(t *testing.T) {
			reg := registries.New()
			tc.register(reg)

			desc, ok := reg.Get(tc.id)
			if !ok {
				t.Fatalf("%s not registered", tc.id)
			}

			mock := &mockChatCompleter{}

			a, err := desc.Factory(registries.FactoryOptions{
				LLM:                 mock,
				Model:               "test-model",
				MaxIterations:       10,
				ProjectInstructions: instructions,
			})
			if err != nil {
				t.Fatalf("Factory: %v", err)
			}

			if a.ID() != tc.id {
				t.Errorf("ID = %q, want %q", a.ID(), tc.id)
			}
		})
	}
}

// captureChatCompleter captures the ChatRequest sent to it.
type captureChatCompleter struct {
	captured *largemodel.Request
	response *largemodel.Response
}

func (c *captureChatCompleter) Protocol() schema.Protocol { return schema.ProtocolOpenAIChat }
func (c *captureChatCompleter) Call(_ context.Context, req *largemodel.Request) (*largemodel.Response, error) {
	c.captured = req

	return c.response, nil
}

func (c *captureChatCompleter) CallStream(_ context.Context, _ *largemodel.Request) (*largemodel.Stream, error) {
	return nil, nil
}

func TestSystemPrompt_ContainsProjectInstructions(t *testing.T) {
	instructions := "UNIQUE_PROJECT_MARKER_12345"

	capture := &captureChatCompleter{
		response: largemodel.FakeStopResponse(schema.ProtocolOpenAIChat, "Hello!", schema.Usage{}),
	}

	reg := registries.New()
	RegisterResearcher(reg)

	desc, _ := reg.Get("researcher")

	a, err := desc.Factory(registries.FactoryOptions{
		LLM:                 capture,
		Model:               "test-model",
		MaxIterations:       1,
		ProjectInstructions: instructions,
	})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}

	ctx := context.Background()

	// Run the agent to trigger an LLM call.
	runReq := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage(schema.ProtocolOpenAIChat, "test prompt"),
		},
	}

	_, runErr := a.Run(ctx, runReq)
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}

	if capture.captured == nil {
		t.Fatal("expected LLM call to be captured")
	}

	// Check the system message contains project instructions.
	if len(capture.captured.Messages) == 0 {
		t.Fatal("expected at least one message in the request")
	}

	systemMsg := capture.captured.Messages[0]
	if systemMsg.Role() != schema.RoleSystem {
		t.Fatalf("first message role = %q, want %q", systemMsg.Role(), schema.RoleSystem)
	}

	text := systemMsg.Text()
	if !strings.Contains(text, instructions) {
		t.Errorf("system prompt should contain project instructions %q, got:\n%s", instructions, text)
	}

	if !strings.Contains(text, "# Project Instructions") {
		t.Errorf("system prompt should contain project instructions header")
	}
}
