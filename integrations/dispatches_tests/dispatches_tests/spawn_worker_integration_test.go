package dispatches_tests

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	vvagents "github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/registries"
)

// =============================================================================
// spawn_worker integration tests.
//
// These drive the whole derivation path end to end: a real Primary taskagent
// emits a spawn_worker tool call, the dispatcher validates the spec, assembles
// a real worker taskagent, injects its declared read-only context, runs it on
// the same mock LLM, and folds the answer back into the Primary's reply.
//
// The mock records every request, so the assertions are about what the worker
// *actually asked the model* — its system prompt, its advertised tools and the
// context it was handed — rather than about internal state.
// =============================================================================

// recordingMockLLM is a sequentialMockLLM that also keeps every request it saw.
//
// It implements CallStream because a derived worker is a real taskagent, i.e. a
// StreamAgent: when the Primary's tool handler finds an emitter on the context
// it relays the worker's own stream (that is how a worker stays expandable in
// the UI). A Call-only mock would never see the worker at all.
type recordingMockLLM struct {
	sequentialMockLLM

	mu       sync.Mutex
	requests []*largemodel.Request
}

func (m *recordingMockLLM) Call(ctx context.Context, req *largemodel.Request) (*largemodel.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()

	return m.sequentialMockLLM.Call(ctx, req)
}

// CallStream replays the next canned response as a two-chunk stream: the
// payload, then a terminal finish chunk.
func (m *recordingMockLLM) CallStream(ctx context.Context, req *largemodel.Request) (*largemodel.Stream, error) {
	resp, err := m.Call(ctx, req)
	if err != nil {
		return nil, err
	}

	chunks := []*largemodel.Chunk{responseChunk(resp), {FinishReason: resp.FinishReason, Usage: &resp.Usage}}
	next := 0

	return largemodel.NewStream(func() (*largemodel.Chunk, error) {
		if next >= len(chunks) {
			return nil, io.EOF
		}

		c := chunks[next]
		next++

		return c, nil
	}, func() error { return nil }), nil
}

// responseChunk turns a canned response into the single content chunk a vendor
// would have streamed for it (text, or tool-call fragments).
func responseChunk(resp *largemodel.Response) *largemodel.Chunk {
	if calls := resp.Message.ToolCalls(); len(calls) > 0 {
		deltas := make([]largemodel.ToolCallDelta, 0, len(calls))
		for i, call := range calls {
			deltas = append(deltas, largemodel.ToolCallDelta{
				Index:          i,
				ID:             call.ID,
				Name:           call.Name,
				ArgumentsDelta: call.Arguments,
			})
		}

		return &largemodel.Chunk{ToolCallDeltas: deltas}
	}

	return &largemodel.Chunk{TextDelta: resp.Message.Text()}
}

func (m *recordingMockLLM) request(i int) *largemodel.Request {
	m.mu.Lock()
	defer m.mu.Unlock()

	if i >= len(m.requests) {
		return nil
	}

	return m.requests[i]
}

// stubDiffSources returns a context source registry whose `diff` provider
// yields a fixed patch, so the test does not depend on the repository state.
func stubDiffSources(diff string) *registries.ContextSourceRegistry {
	reg := registries.NewContextSources()
	reg.MustRegister(registries.ContextSource{
		ID:          registries.ContextSourceDiff,
		Description: "stub working tree diff",
		Provider:    func(context.Context) (string, error) { return diff, nil },
	})

	return reg
}

// newSpawnWorkerDispatcher wires a unified-mode Dispatcher whose Primary
// carries spawn_worker (plus the delegate family, to prove both coexist).
func newSpawnWorkerDispatcher(t *testing.T, mockLLM largemodel.Caller, opts ...dispatches.Option) *dispatches.Dispatcher {
	t.Helper()

	// The production descriptors: worker assembly must resolve the same
	// ToolProfiles and prompts setup wires at startup.
	reg := registries.New()
	vvagents.RegisterCoder(reg)
	vvagents.RegisterResearcher(reg)
	vvagents.RegisterReviewer(reg)

	base := []dispatches.Option{
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithToolsConfig(configs.ToolsConfig{}),
		dispatches.WithMaxIterations(3),
		dispatches.WithSkills(registries.DefaultSkills()),
	}

	d := dispatches.New(reg, map[string]agent.Agent{}, nil, append(base, opts...)...)

	toolReg := tool.NewRegistry()
	if _, err := dispatches.RegisterSpawnWorkerTool(toolReg, d); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	primary := taskagent.New(
		agent.Config{ID: vvagents.PrimaryAgentID, Name: "Primary Assistant", Description: "test primary"},
		taskagent.WithCaller(mockLLM),
		taskagent.WithModel("test-model"),
		taskagent.WithSystemPrompt(prompt.StringPrompt(vvagents.PrimarySystemPrompt)),
		taskagent.WithToolRegistry(toolReg),
		taskagent.WithMaxIterations(5),
	)

	d.SetPrimaryAssistant(primary)

	return d
}

func requestToolNames(req *largemodel.Request) []string {
	names := make([]string, 0, len(req.Tools))
	for _, def := range req.Tools {
		names = append(names, def.Name)
	}

	slices.Sort(names)

	return names
}

func requestText(req *largemodel.Request) string {
	var sb strings.Builder

	for _, m := range req.Messages {
		fmt.Fprintf(&sb, "[%s] %s\n", m.Role(), m.Text())
	}

	return sb.String()
}

// TestSpawnWorker_CodeReviewCombination is the end-to-end proof that a code
// review needs no new agent type: the Primary derives "coder runtime + review
// skill + Review tool access + diff context" and the worker that actually runs
// has exactly that capability surface.
func TestSpawnWorker_CodeReviewCombination(t *testing.T) {
	const diff = "```diff\n--- a/pay.go\n+++ b/pay.go\n+\tamount := price * qty\n```"

	mockLLM := &recordingMockLLM{
		sequentialMockLLM: sequentialMockLLM{
			responses: []*largemodel.Response{
				// 1. Primary derives the code-review worker.
				primaryToolCallResponse(dispatches.PrimaryToolSpawnWorker,
					`{"task":"review the change","base_type":"coder","tool_access":"review",`+
						`"skills":["review"],"context_sources":["diff"]}`),
				// 2. The worker answers (this is the worker's own LLM call).
				primaryTextResponse("blocker: pay.go:3 overflow risk on price * qty"),
				// 3. Primary folds the worker's finding into its reply.
				primaryTextResponse("Review found 1 blocker in pay.go."),
			},
		},
	}

	d := newSpawnWorkerDispatcher(t, mockLLM, dispatches.WithContextSources(stubDiffSources(diff)))

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "review my change")},
		SessionID: "spawn-code-review",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 3 {
		t.Fatalf("LLM call count = %d, want 3 (primary → worker → primary)", got)
	}

	// Request 2 is the worker's: assert its actual capability surface.
	workerReq := mockLLM.request(1)
	if workerReq == nil {
		t.Fatal("worker never called the model")
	}

	tools := requestToolNames(workerReq)

	for _, want := range []string{"read", "grep", "glob", "bash"} {
		if !slices.Contains(tools, want) {
			t.Errorf("code-review worker missing %q; advertised %v", want, tools)
		}
	}

	for _, forbidden := range []string{"write", "edit"} {
		if slices.Contains(tools, forbidden) {
			t.Errorf("code-review worker must not advertise %q; advertised %v", forbidden, tools)
		}
	}

	// The review skill rides on the coder runtime's own prompt.
	body := requestText(workerReq)

	if !strings.Contains(body, registries.ReviewSkillInstructions) {
		t.Error("worker prompt is missing the review skill instructions")
	}

	// The coder runtime's own prompt advertises write/edit. Under Review
	// access the worker has neither, so the assembly must correct the prompt
	// in-band — otherwise the model burns iterations calling missing tools.
	if !strings.Contains(body, "Effective tool access: review") {
		t.Error("worker prompt does not announce the narrowed effective tool set")
	}

	for _, want := range []string{"read", "bash"} {
		if !strings.Contains(body, want) {
			t.Errorf("effective-tools notice missing %q", want)
		}
	}

	if !strings.Contains(body, "write / edit") {
		t.Error("effective-tools notice does not call out that write/edit are unavailable")
	}

	// The diff arrived as an explicit read-only block, not as prose the
	// Primary happened to paraphrase.
	for _, want := range []string{"## Context: diff (read-only)", "price * qty"} {
		if !strings.Contains(body, want) {
			t.Errorf("worker input missing %q", want)
		}
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Text(), "1 blocker") {
		t.Errorf("Primary did not fold the worker result: %+v", resp.Messages)
	}
}

// A worker derived with tool_access "none" is LLM-only: no tools reach the
// model at all.
func TestSpawnWorker_NoneProfileIsToolFree(t *testing.T) {
	mockLLM := &recordingMockLLM{
		sequentialMockLLM: sequentialMockLLM{
			responses: []*largemodel.Response{
				primaryToolCallResponse(dispatches.PrimaryToolSpawnWorker,
					`{"task":"summarise the tradeoffs","base_type":"researcher","tool_access":"none"}`),
				primaryTextResponse("tradeoffs: a vs b"),
				primaryTextResponse("Summarised."),
			},
		},
	}

	d := newSpawnWorkerDispatcher(t, mockLLM)

	if _, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "summarise")},
		SessionID: "spawn-none",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	workerReq := mockLLM.request(1)
	if workerReq == nil {
		t.Fatal("worker never called the model")
	}

	if len(workerReq.Tools) != 0 {
		t.Errorf("none-profile worker advertised tools: %v", requestToolNames(workerReq))
	}
}

// An invalid spec folds back to the Primary as a diagnosable tool error, and no
// worker ever calls the model.
func TestSpawnWorker_InvalidSpecFoldsBackToPrimary(t *testing.T) {
	mockLLM := &recordingMockLLM{
		sequentialMockLLM: sequentialMockLLM{
			responses: []*largemodel.Response{
				primaryToolCallResponse(dispatches.PrimaryToolSpawnWorker,
					`{"task":"review","base_type":"code-reviewer"}`),
				primaryTextResponse("That agent type does not exist; reviewing inline instead."),
			},
		},
	}

	d := newSpawnWorkerDispatcher(t, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "review")},
		SessionID: "spawn-invalid",
	})
	if err != nil {
		t.Fatalf("Run must not abort on an invalid spec: %v", err)
	}

	// Exactly two calls: the spawn attempt and the Primary's recovery. A
	// third would mean a worker ran despite the invalid spec.
	if got := int(mockLLM.callCount.Load()); got != 2 {
		t.Fatalf("LLM call count = %d, want 2 (no worker may run on an invalid spec)", got)
	}

	// The Primary's second request carries the tool error verbatim, which is
	// what lets it recover rather than guess.
	recovery := requestText(mockLLM.request(1))
	if !strings.Contains(recovery, "spawn_worker:") || !strings.Contains(recovery, "invalid base_type") {
		t.Errorf("Primary did not receive a diagnosable tool error:\n%s", recovery)
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Text(), "inline instead") {
		t.Errorf("unexpected recovery response: %+v", resp.Messages)
	}
}
