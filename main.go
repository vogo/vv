package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
	"github.com/vogo/vv/setup"
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
		slog.Error("vv: load config", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	// If required config is missing, prompt the user interactively.
	if configs.NeedsSetup(cfg) {
		fmt.Println("No configuration found. Please provide the following values:")
		fmt.Println()

		if err := configs.Prompt(cfg, *configPath, os.Stdin, os.Stdout); err != nil {
			slog.Error("vv: save config", "error", err)
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

	// Initialize LLM client, memory, and agents via setup package.
	confirmTools := cfg.CLI.ConfirmTools
	initResult, err := setup.Init(cfg, &setup.Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			return cli.WrapRegistry(r, confirmTools)
		},
	})
	if err != nil {
		slog.Error("vv: init", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cfg.Mode {
	case "http":
		if err := httpapis.Serve(ctx, cfg, initResult.SetupResult.Dispatcher, initResult.SetupResult.Agents(), initResult.PersistentMem); err != nil {
			slog.Error("vv: HTTP server error", "error", err)
			os.Exit(1)
		}

	default: // "cli" or any other value defaults to CLI mode.
		app := cli.New(initResult.SetupResult.Dispatcher, cfg, initResult.PersistentMem)
		if err := app.Run(ctx); err != nil {
			slog.Error("vv: CLI error", "error", err)
			os.Exit(1)
		}
	}
}
