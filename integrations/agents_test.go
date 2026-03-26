package integrations

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vagents/vaga/agents"
	"github.com/vogo/vagents/vaga/config"
	"github.com/vogo/vagents/vaga/tools"
)

func TestIntegration_Agents_CoderHasTools(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	coder, _ := agents.Create(cfg, mock, reg)
	toolList := coder.Tools()

	if len(toolList) != 6 {
		t.Fatalf("coder has %d tools, want 6", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "file_read", "file_write", "file_edit", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("coder missing tool %q", name)
		}
	}
}

func TestIntegration_Agents_ChatHasNoTools(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
	}

	_, chat := agents.Create(cfg, mock, reg)
	toolList := chat.Tools()

	if len(toolList) != 0 {
		t.Errorf("chat has %d tools, want 0", len(toolList))
	}
}

func TestIntegration_Agents_RouterRoutesCorrectly(t *testing.T) {
	tests := []struct {
		name         string
		llmResponse  string
		input        string
		wantResponse string
	}{
		{
			name:         "routes to coder for code request",
			llmResponse:  "0",
			input:        "write a function to sort an array",
			wantResponse: "coder handled it",
		},
		{
			name:         "routes to chat for general request",
			llmResponse:  "1",
			input:        "what is the capital of France",
			wantResponse: "chat handled it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockChatCompleter{
				response: &aimodel.ChatResponse{
					Choices: []aimodel.Choice{
						{
							Message: aimodel.Message{
								Role:    aimodel.RoleAssistant,
								Content: aimodel.NewTextContent(tt.llmResponse),
							},
						},
					},
				},
			}

			cfg := &config.Config{
				LLM:    config.LLMConfig{Model: "test-model"},
				Agents: config.AgentsConfig{MaxIterations: 10},
			}

			coder := &stubAgent{
				id: "coder",
				response: &schema.RunResponse{
					Messages: []schema.Message{
						schema.NewAssistantMessage(aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent("coder handled it"),
						}, "coder"),
					},
				},
			}
			chat := &stubAgent{
				id: "chat",
				response: &schema.RunResponse{
					Messages: []schema.Message{
						schema.NewAssistantMessage(aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent("chat handled it"),
						}, "chat"),
					},
				},
			}

			router := agents.CreateRouter(cfg, mock, coder, chat)

			resp, err := router.Run(context.Background(), &schema.RunRequest{
				Messages: []schema.Message{schema.NewUserMessage(tt.input)},
			})
			if err != nil {
				t.Fatalf("router.Run: %v", err)
			}

			if len(resp.Messages) == 0 {
				t.Fatal("expected at least one response message")
			}

			text := resp.Messages[0].Content.Text()
			if text != tt.wantResponse {
				t.Errorf("response = %q, want %q", text, tt.wantResponse)
			}
		})
	}
}
