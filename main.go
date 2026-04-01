package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/service"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/memories"
	"github.com/vogo/vv/setup"
	"github.com/vogo/vv/tools"
)

func main() {
	// Parse flags.
	configPath := flag.String("config", configs.DefaultPath(), "config file path")
	listenAddr := flag.String("addr", "", "listen address (overrides config)")
	modeFlag := flag.String("mode", "", "run mode: cli or http (default: cli)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment variables:\n")
		fmt.Fprintf(os.Stderr, "  VV_LLM_API_KEY      LLM API key (required if not in config file)\n")
		fmt.Fprintf(os.Stderr, "  VV_LLM_BASE_URL     LLM base URL\n")
		fmt.Fprintf(os.Stderr, "  VV_LLM_MODEL        LLM model name\n")
		fmt.Fprintf(os.Stderr, "  VV_LLM_PROVIDER     LLM provider (openai, anthropic)\n")
		fmt.Fprintf(os.Stderr, "  VV_SERVER_ADDR      Server listen address\n")
		fmt.Fprintf(os.Stderr, "  VV_MODE             Run mode (cli or http)\n")
		fmt.Fprintf(os.Stderr, "\nConfig file: %s\n", configs.DefaultPath())
	}
	flag.Parse()

	// Determine whether the config path was explicitly set by the user.
	explicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicit = true
		}
	})

	// Load configs.
	cfg, err := configs.Load(*configPath, explicit)
	if err != nil {
		slog.Error("vaga: load config", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	// If required config is missing, prompt the user interactively.
	if configs.NeedsSetup(cfg) {
		fmt.Println("No configuration found. Please provide the following values:")
		fmt.Println()

		if err := configs.Prompt(cfg, *configPath, os.Stdin, os.Stdout); err != nil {
			slog.Error("vaga: save config", "error", err)
			os.Exit(1)
		}

		fmt.Printf("\nConfiguration saved to %s\n\n", *configPath)
	}

	if *modeFlag != "" {
		cfg.Mode = *modeFlag
	}

	if *listenAddr != "" {
		cfg.Server.Addr = *listenAddr
	}

	// Capture working directory at startup.
	if cfg.Tools.BashWorkingDir == "" {
		workingDir, wdErr := os.Getwd()
		if wdErr != nil {
			workingDir = "."
		}
		cfg.Tools.BashWorkingDir = workingDir
	}

	// Create LLM client.
	llmClient, err := configs.NewLLMClient(cfg.LLM)
	if err != nil {
		slog.Error("vaga: create LLM client", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	// Register tools (full registry for HTTP service).
	toolRegistry, err := tools.Register(cfg.Tools)
	if err != nil {
		slog.Error("vaga: register tools", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	// Create persistent memory with FileStore backend.
	fileStore, err := memories.NewFileStore(cfg.Memory.Dir)
	if err != nil {
		slog.Error("vaga: create file store", "error", err)
		os.Exit(1)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(fileStore)

	// Create memory manager.
	memMgr := memory.NewManager(
		memory.WithStore(persistentMem),
		memory.WithPromoter(memory.PromoteAll()),
		memory.WithCompressor(memory.NewSlidingWindowCompressor(cfg.Memory.SessionWindow)),
	)

	// Set up all agents via setup package.
	confirmTools := cfg.CLI.ConfirmTools
	result, err := setup.New(cfg, llmClient, memMgr, persistentMem, &setup.Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			return cli.WrapRegistry(r, confirmTools)
		},
	})
	if err != nil {
		slog.Error("vaga: setup agents", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cfg.Mode {
	case "http":
		// Create and start HTTP service.
		svc := service.New(
			service.Config{Addr: cfg.Server.Addr},
			service.WithToolRegistry(toolRegistry),
		)
		svc.RegisterAgent(result.Dispatcher)
		for _, a := range result.Agents() {
			svc.RegisterAgent(a)
		}

		// Build a custom mux that wraps the service handler with memory endpoints.
		svcHandler := svc.Handler()
		mux := http.NewServeMux()
		mux.Handle("/", svcHandler)
		mux.HandleFunc("GET /v1/memory", handleListMemory(persistentMem))
		mux.HandleFunc("GET /v1/memory/{namespace}/{key}", handleGetMemory(persistentMem))
		mux.HandleFunc("PUT /v1/memory/{namespace}/{key}", handleSetMemory(persistentMem))
		mux.HandleFunc("DELETE /v1/memory/{namespace}/{key}", handleDeleteMemory(persistentMem))

		slog.Info("vaga: starting HTTP server", "addr", cfg.Server.Addr)

		ln, err := net.Listen("tcp", cfg.Server.Addr)
		if err != nil {
			slog.Error("vaga: listen", "error", err)
			os.Exit(1)
		}

		server := &http.Server{Handler: mux}

		// Shut down when context is canceled.
		go func() {
			<-ctx.Done()
			_ = server.Shutdown(context.Background())
		}()

		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("vaga: server error", "error", err)
			os.Exit(1)
		}

		slog.Info("vaga: shutdown complete")

	default: // "cli" or any other value defaults to CLI mode.
		app := cli.New(result.Dispatcher, cfg, persistentMem)
		if err := app.Run(ctx); err != nil {
			slog.Error("vaga: CLI error", "error", err)
			os.Exit(1)
		}
	}
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
		entries, err := mem.List(r.Context(), prefix)
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

		val, err := mem.Get(r.Context(), fullKey)
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

		var req memorySetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}

		if err := mem.Set(r.Context(), fullKey, req.Content, 0); err != nil {
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

		// Check existence first.
		val, err := mem.Get(r.Context(), fullKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}
		if val == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "memory entry not found"})
			return
		}

		if err := mem.Delete(r.Context(), fullKey); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
