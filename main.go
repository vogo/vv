package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/askuser"
	"github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/debugs"
	"github.com/vogo/vv/httpapis"
	"github.com/vogo/vv/setup"
)

func main() {
	// Parse flags.
	configPath := flag.String("config", configs.DefaultPath(), "config file path")
	listenAddr := flag.String("addr", "", "listen address (overrides config)")
	modeFlag := flag.String("mode", "", "run mode: cli or http (default: cli)")
	promptFlag := flag.String("p", "", "run a single prompt non-interactively and exit")
	permissionModeFlag := flag.String("permission-mode", "", "tool permission mode: default, accept-edits, auto, plan")
	debugFlag := flag.Bool("debug", false, "enable detailed LLM and tool I/O debug records (env: VV_DEBUG)")
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
		fmt.Fprintf(os.Stderr, "  VV_DEBUG            Enable debug records (true/false). Equivalent to --debug.\n")
		fmt.Fprintf(os.Stderr, "  VV_DEBUG_FILE       Override debug log file path (interactive CLI only).\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  vv                                          # interactive TUI mode\n")
		fmt.Fprintf(os.Stderr, "  vv -p \"explain the main.go file\"             # single prompt, exit after response\n")
		fmt.Fprintf(os.Stderr, "  vv -p \"fix the bug in auth.go\" 2>/dev/null   # suppress diagnostics\n")
		fmt.Fprintf(os.Stderr, "\nConfig file: %s\n", configs.DefaultPath())
	}
	flag.Parse()

	// Determine whether flags were explicitly set by the user.
	explicit := false
	promptSet := false
	debugSet := false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "config":
			explicit = true
		case "p":
			promptSet = true
		case "debug":
			debugSet = true
		}
	})

	// Load configs.
	cfg, err := configs.Load(*configPath, explicit)
	if err != nil {
		slog.Error("vv: load config", "error", err)
		flag.Usage()
		os.Exit(1)
	}

	// If required config is missing, prompt the user interactively or fail fast for -p mode.
	if configs.NeedsSetup(cfg) {
		if promptSet {
			fmt.Fprintf(os.Stderr, "vv: configuration incomplete (missing API key); "+
				"run `vv` interactively to set up, or set VV_LLM_API_KEY\n")
			os.Exit(1)
		}

		fmt.Println("No configuration found. Please provide the following values:")
		fmt.Println()

		if err := configs.Prompt(cfg, *configPath, os.Stdin, os.Stdout); err != nil {
			slog.Error("vv: save config", "error", err)
			os.Exit(1)
		}

		fmt.Printf("\nConfiguration saved to %s\n\n", *configPath)
	}

	// Debug precedence: CLI > env (already in cfg.Debug from configs.Load) > YAML > false.
	if debugSet {
		cfg.Debug = *debugFlag
	}

	// Construct the debug sink (only when enabled). Sink mode is decided here:
	// HTTP -> slog, CLI -p -> stderr, CLI interactive -> file.
	var debugSink *debugs.Sink
	if cfg.Debug {
		switch {
		case (*modeFlag == "http") || (cfg.Mode == "http" && *modeFlag == ""):
			debugSink = debugs.NewSlogSink(slog.Default())
		case promptSet:
			debugSink = debugs.NewWriterSink(os.Stderr)
		default:
			path := debugs.DefaultFilePath()
			s, derr := debugs.NewFileSink(path)
			if derr != nil {
				slog.Error("vv: open debug file", "error", derr)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "vv: debug enabled, log file: %s\n", path)
			debugSink = s
		}
	}
	if debugSink != nil {
		defer func() { _ = debugSink.Close() }()
	}

	if *modeFlag != "" {
		cfg.Mode = *modeFlag
	}

	if *listenAddr != "" {
		cfg.Server.Addr = *listenAddr
	}

	// Single-prompt mode: run the prompt non-interactively and exit.
	if promptSet {
		trimmed := strings.TrimSpace(*promptFlag)
		if trimmed == "" {
			fmt.Fprintf(os.Stderr, "vv: -p flag requires a non-empty prompt\n")
			os.Exit(1)
		}

		if cfg.Mode == "http" {
			fmt.Fprintf(os.Stderr, "vv: -p flag is incompatible with HTTP mode\n")
			os.Exit(1)
		}

		initResult, initErr := setup.Init(cfg, &setup.Options{
			UserInteractor: askuser.NonInteractiveInteractor{},
			AskUserTimeout: time.Duration(cfg.Agents.AskUserTimeout) * time.Second,
			DebugSink:      debugSink,
		})
		if initErr != nil {
			slog.Error("vv: init", "error", initErr)
			os.Exit(1)
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if runErr := cli.RunPrompt(ctx, initResult.SetupResult.Dispatcher, trimmed, os.Stdout, os.Stderr); runErr != nil {
			fmt.Fprintf(os.Stderr, "vv: %s\n", runErr)
			os.Exit(1)
		}

		os.Exit(0)
	}

	// Initialize LLM client, memory, and agents via setup package.
	askUserTimeout := time.Duration(cfg.Agents.AskUserTimeout) * time.Second
	cliInteractor := cli.NewCLIInteractor()

	// Determine interactor based on run mode.
	var interactor askuser.UserInteractor = cliInteractor

	if cfg.Mode == "http" {
		interactor = askuser.NonInteractiveInteractor{}
	}

	// Permission mode: flag > env > yaml > default.
	permissionMode := cfg.CLI.PermissionMode
	if *permissionModeFlag != "" {
		permissionMode = configs.PermissionMode(*permissionModeFlag)
		if !configs.IsValidPermissionMode(permissionMode) {
			slog.Error("vv: invalid --permission-mode", "value", *permissionModeFlag)
			os.Exit(1)
		}
	}

	permissionState := cli.NewPermissionState(permissionMode)
	permissionState.SetClassifier(configs.BuildBashClassifier(cfg.Tools.BashRules))
	permissionState.SetNonInteractive(cfg.Mode == "http")

	initResult, err := setup.Init(cfg, &setup.Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			return cli.WrapRegistryWithPermission(r, permissionState)
		},
		UserInteractor: interactor,
		AskUserTimeout: askUserTimeout,
		DebugSink:      debugSink,
	})
	if err != nil {
		slog.Error("vv: init", "error", err)
		os.Exit(1)
	}

	permissionState.SetPathGuardian(initResult.SetupResult.PathGuardian)

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cfg.Mode {
	case "http":
		interactionStore := httpapis.NewInteractionStore(ctx, askUserTimeout)
		if err := httpapis.Serve(ctx, cfg, initResult.SetupResult.Dispatcher, initResult.SetupResult.Agents(), initResult.PersistentMem, interactionStore, initResult.Compactor); err != nil {
			slog.Error("vv: HTTP server error", "error", err)
			os.Exit(1)
		}

	default: // "cli" or any other value defaults to CLI mode.
		app := cli.New(initResult.SetupResult.Dispatcher, cfg, initResult.PersistentMem, cliInteractor, initResult.Compactor,
			cli.WithPermissionState(permissionState))
		if err := app.Run(ctx); err != nil {
			slog.Error("vv: CLI error", "error", err)
			os.Exit(1)
		}
	}
}
