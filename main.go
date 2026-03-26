package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/vogo/vage/service"
	"github.com/vogo/vagents/vaga/agents"
	"github.com/vogo/vagents/vaga/config"
	"github.com/vogo/vagents/vaga/tools"
)

func main() {
	// Parse flags.
	configPath := flag.String("config", config.DefaultPath(), "config file path")
	listenAddr := flag.String("addr", "", "listen address (overrides config)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment variables:\n")
		fmt.Fprintf(os.Stderr, "  VAGA_LLM_API_KEY      LLM API key (required if not in config file)\n")
		fmt.Fprintf(os.Stderr, "  VAGA_LLM_BASE_URL     LLM base URL\n")
		fmt.Fprintf(os.Stderr, "  VAGA_LLM_MODEL        LLM model name\n")
		fmt.Fprintf(os.Stderr, "  VAGA_LLM_PROVIDER     LLM provider (openai, anthropic)\n")
		fmt.Fprintf(os.Stderr, "  VAGA_SERVER_ADDR      Server listen address\n")
		fmt.Fprintf(os.Stderr, "\nConfig file: %s\n", config.DefaultPath())
	}
	flag.Parse()

	// Determine whether the config path was explicitly set by the user.
	explicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicit = true
		}
	})

	// Load config.
	cfg, err := config.Load(*configPath, explicit)
	if err != nil {
		slog.Error("vaga: load config", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	// If required config is missing, prompt the user interactively.
	if config.NeedsSetup(cfg) {
		fmt.Println("No configuration found. Please provide the following values:")
		fmt.Println()

		if err := config.Prompt(cfg, *configPath, os.Stdin, os.Stdout); err != nil {
			slog.Error("vaga: save config", "error", err)
			os.Exit(1)
		}

		fmt.Printf("\nConfiguration saved to %s\n\n", *configPath)
	}

	if *listenAddr != "" {
		cfg.Server.Addr = *listenAddr
	}

	// Create LLM client.
	llmClient, err := config.NewLLMClient(cfg.LLM)
	if err != nil {
		slog.Error("vaga: create LLM client", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	slog.Info("vaga: LLM client created",
		"provider", cfg.LLM.Provider,
		"model", cfg.LLM.Model,
	)

	// Register tools.
	toolRegistry, err := tools.Register(cfg.Tools)
	if err != nil {
		slog.Error("vaga: register tools", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	slog.Info("vaga: tools registered", "count", len(toolRegistry.List()))

	// Create agents.
	coderAgent, chatAgent := agents.Create(cfg, llmClient, toolRegistry)
	router := agents.CreateRouter(cfg, llmClient, coderAgent, chatAgent)

	slog.Info("vaga: agents created",
		"agents", []string{router.ID(), coderAgent.ID(), chatAgent.ID()},
	)

	// Create and start service.
	svc := service.New(
		service.Config{Addr: cfg.Server.Addr},
		service.WithToolRegistry(toolRegistry),
	)
	svc.RegisterAgent(router)
	svc.RegisterAgent(coderAgent)
	svc.RegisterAgent(chatAgent)

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("vaga: starting", "addr", cfg.Server.Addr)

	if err := svc.Start(ctx); err != nil {
		slog.Error("vaga: server error", "error", err)
		os.Exit(1)
	}

	slog.Info("vaga: shutdown complete")
}
