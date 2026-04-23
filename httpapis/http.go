package httpapis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/service"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/debugs"
	"github.com/vogo/vv/memories"
	"github.com/vogo/vv/tools"
	"github.com/vogo/vv/traces/budgets"
	"github.com/vogo/vv/traces/costtraces"
)

// requestIDMiddleware injects a fresh debug request id into the request
// context. Always installed (not gated on debug) to keep the conditional
// surface minimal and avoid any chance of body drift between debug on/off.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := debugs.WithRequestID(r.Context(), newRequestID())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [12]byte
	if _, err := cryptoRandRead(b[:]); err != nil {
		return ""
	}
	return hexEncode(b[:])
}

// Serve starts the HTTP server with agent and memory endpoints.
// It blocks until the context is canceled or a fatal error occurs.
// compactor may be nil if context compression is not configured.
func Serve(ctx context.Context, cfg *configs.Config, llm aimodel.ChatCompleter, dispatcher agent.Agent, agents []agent.Agent, persistentMem memory.Memory, interactionStore *InteractionStore, compactor *memory.ConversationCompactor, sessionBudget, dailyBudget *budgets.Tracker) error {
	// Register tools (full registry for HTTP service).
	toolRegistry, err := tools.Register(cfg.Tools)
	if err != nil {
		return fmt.Errorf("register tools: %w", err)
	}

	// Create HTTP service.
	svc := service.New(
		service.Config{Addr: cfg.Server.Addr},
		service.WithToolRegistry(toolRegistry),
	)
	svc.RegisterAgent(dispatcher)
	for _, a := range agents {
		svc.RegisterAgent(a)
	}

	// Build pricing lookup for cost enrichment middleware.
	customPricing := configs.ConvertPricing(cfg.ModelPricing)
	pricingLookup := func(model string) *costtraces.Pricing {
		return costtraces.LookupPricing(model, customPricing)
	}

	// Build a custom mux that wraps the service handler with cost enrichment
	// and budget-error (429) middlewares. Budget middleware is inner so cost
	// enrichment still runs on non-budget responses. The budget middleware is
	// only installed when at least one tracker is active — otherwise the extra
	// body-buffering layer would pay for itself on every request for no gain.
	inner := svc.Handler()
	if sessionBudget != nil || dailyBudget != nil {
		inner = budgetErrorMiddleware(inner)
	}
	svcHandler := costEnrichMiddleware(inner, pricingLookup)
	mux := http.NewServeMux()
	mux.Handle("/", svcHandler)
	mux.HandleFunc("GET /v1/memory", handleListMemory(persistentMem))
	mux.HandleFunc("GET /v1/memory/{namespace}/{key}", handleGetMemory(persistentMem))
	mux.HandleFunc("PUT /v1/memory/{namespace}/{key}", handleSetMemory(persistentMem))
	mux.HandleFunc("DELETE /v1/memory/{namespace}/{key}", handleDeleteMemory(persistentMem))

	if interactionStore != nil {
		mux.HandleFunc("POST /v1/interactions/{interactionID}/respond", handleInteractionRespond(interactionStore))
	}

	if cfg.Eval.Enabled {
		mux.HandleFunc("POST /v1/eval/run", handleEvalRun(cfg, dispatcher, llm))
	}

	if sessionBudget != nil || dailyBudget != nil {
		mux.HandleFunc("GET /v1/budget", handleGetBudget(sessionBudget, dailyBudget))
	}

	slog.Info("vv: starting HTTP server", "addr", cfg.Server.Addr)

	ln, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Server.Addr, err)
	}

	server := &http.Server{Handler: requestIDMiddleware(mux)}

	// Shut down when context is canceled.
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}

	slog.Info("vv: shutdown complete")

	return nil
}

// HTTP Memory Endpoints

type memorySetRequest struct {
	Content string `json:"content"`
}

type memoryListResponse struct {
	Entries []memoryEntryResponse `json:"entries"`
}

type memoryEntryResponse struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func handleListMemory(mem memory.Memory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := r.URL.Query().Get("namespace")
		prefix := ns
		ctx := memories.WithUserPath(r.Context())
		entries, err := mem.List(ctx, prefix)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}

		resp := memoryListResponse{Entries: make([]memoryEntryResponse, len(entries))}
		for i, e := range entries {
			eNs, eKey := splitKey(e.Key)
			resp.Entries[i] = memoryEntryResponse{
				Namespace: eNs,
				Key:       eKey,
				Content:   fmt.Sprintf("%v", e.Value),
				CreatedAt: e.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func handleGetMemory(mem memory.Memory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := r.PathValue("namespace")
		key := r.PathValue("key")
		fullKey := ns + ":" + key

		ctx := memories.WithUserPath(r.Context())
		val, err := mem.Get(ctx, fullKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}

		if val == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "memory entry not found"})
			return
		}

		writeJSON(w, http.StatusOK, memoryEntryResponse{
			Namespace: ns,
			Key:       key,
			Content:   fmt.Sprintf("%v", val),
		})
	}
}

func handleSetMemory(mem memory.Memory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := r.PathValue("namespace")
		key := r.PathValue("key")
		fullKey := ns + ":" + key

		if !memories.IsSharedNamespace(ns) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"code":    "forbidden",
				"message": fmt.Sprintf("namespace %q is session-private; HTTP memory endpoints are limited to shared namespaces", ns),
			})
			return
		}

		var req memorySetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}

		ctx := memories.WithUserPath(r.Context())
		if err := mem.Set(ctx, fullKey, req.Content, 0); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, memoryEntryResponse{
			Namespace: ns,
			Key:       key,
			Content:   req.Content,
		})
	}
}

func handleDeleteMemory(mem memory.Memory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := r.PathValue("namespace")
		key := r.PathValue("key")
		fullKey := ns + ":" + key

		if !memories.IsSharedNamespace(ns) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"code":    "forbidden",
				"message": fmt.Sprintf("namespace %q is session-private; HTTP memory endpoints are limited to shared namespaces", ns),
			})
			return
		}

		ctx := memories.WithUserPath(r.Context())

		// Check existence first.
		val, err := mem.Get(ctx, fullKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}
		if val == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "memory entry not found"})
			return
		}

		if err := mem.Delete(ctx, fullKey); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

type interactionRespondRequest struct {
	Response string `json:"response"`
}

func handleInteractionRespond(store *InteractionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		interactionID := r.PathValue("interactionID")

		var req interactionRespondRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}

		if err := store.Respond(interactionID, req.Response); err != nil {
			// Distinguish between not found and already responded using the error
			// message directly, avoiding a second store lookup that could race with
			// the cleanup goroutine.
			if strings.Contains(err.Error(), "already responded") {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "conflict", "message": err.Error()})
			} else {
				writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": err.Error()})
			}

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func splitKey(key string) (string, string) {
	for i, c := range key {
		if c == ':' {
			return key[:i], key[i+1:]
		}
	}
	return "default", key
}
