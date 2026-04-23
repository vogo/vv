package askuser_tests //nolint:revive // integration test package

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
)

// --- Helpers ---

// mockChatCompleter is a simple mock for testing.
type mockChatCompleter struct {
	response *aimodel.ChatResponse
	err      error
}

func (m *mockChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}

	return m.response, nil
}

func (m *mockChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, m.err
}

func testConfig() *configs.Config {
	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "test-model",
			APIKey:   "test-key",
			BaseURL:  "https://api.openai.com/v1",
		},
		Server: configs.ServerConfig{Addr: ":0"},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
		Agents: configs.AgentsConfig{MaxIterations: 5, AskUserTimeout: 300},
	}

	return cfg
}

func testLLM() *mockChatCompleter {
	return &mockChatCompleter{
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
}

// setupInteractionServer creates an httptest.Server with the interaction respond endpoint.
func setupInteractionServer(t *testing.T, store *httpapis.InteractionStore) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/interactions/{interactionID}/respond", func(w http.ResponseWriter, r *http.Request) {
		interactionID := r.PathValue("interactionID")

		var req struct {
			Response string `json:"response"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "bad_request", "message": "invalid request body"})

			return
		}

		if err := store.Respond(interactionID, req.Response); err != nil {
			if strings.Contains(err.Error(), "already responded") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "conflict", "message": err.Error()})
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": err.Error()})
			}

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return ts
}
