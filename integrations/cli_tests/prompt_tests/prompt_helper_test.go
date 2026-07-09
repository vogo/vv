package prompt_tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// stubStreamAgent implements agent.StreamAgent for testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string          { return s.id }
func (s *stubStreamAgent) Name() string        { return s.id }
func (s *stubStreamAgent) Description() string { return s.id }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 8, func(_ context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, req.SessionID, schema.AgentStartData{})); err != nil {
			return err
		}

		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}

// --- Helpers ---

// mockPromptStreamAgent implements agent.StreamAgent with a configurable producer
// for prompt integration testing.
type mockPromptStreamAgent struct {
	id       string
	producer func(ctx context.Context, send func(schema.Event) error) error
}

func (m *mockPromptStreamAgent) ID() string          { return m.id }
func (m *mockPromptStreamAgent) Name() string        { return m.id }
func (m *mockPromptStreamAgent) Description() string { return m.id }

func (m *mockPromptStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{}, nil
}

func (m *mockPromptStreamAgent) RunStream(ctx context.Context, _ *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 16, m.producer), nil
}

// buildVVBinary compiles the vv binary into a temp directory and returns its path.
// The caller is responsible for cleaning up the binary (defer os.Remove).
func buildVVBinary(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	binary := filepath.Join(dir, "vv")

	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = projectRoot()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build vv binary: %v\n%s", err, string(out))
	}

	return binary
}

// projectRoot returns the vv module root directory (the one containing go.mod).
// It is located dynamically by walking up from this test file's location so the
// tests work on any machine and checkout path.
func projectRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("projectRoot: unable to determine caller location")
	}

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			panic("projectRoot: go.mod not found walking up from " + file)
		}
		dir = parent
	}
}

// filterEnv returns a copy of env with the named variables removed.
func filterEnv(env []string, remove ...string) []string {
	removeSet := make(map[string]bool, len(remove))
	for _, r := range remove {
		removeSet[r] = true
	}

	var result []string
	for _, e := range env {
		key := e
		if before, _, ok := strings.Cut(e, "="); ok {
			key = before
		}
		if !removeSet[key] {
			result = append(result, e)
		}
	}

	return result
}
