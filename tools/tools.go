package tools

import (
	"fmt"
	"time"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vage/tool/edit"
	"github.com/vogo/vage/tool/glob"
	"github.com/vogo/vage/tool/grep"
	"github.com/vogo/vage/tool/read"
	"github.com/vogo/vage/tool/write"
	"github.com/vogo/vv/configs"
)

// Register creates a tool registry and registers all built-in tools.
func Register(cfg configs.ToolsConfig) (*tool.Registry, error) {
	reg := tool.NewRegistry()

	// bash
	var bashOpts []bash.Option
	if cfg.BashTimeout > 0 {
		bashOpts = append(bashOpts, bash.WithTimeout(time.Duration(cfg.BashTimeout)*time.Second))
	}

	if cfg.BashWorkingDir != "" {
		bashOpts = append(bashOpts, bash.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := bash.Register(reg, bashOpts...); err != nil {
		return nil, fmt.Errorf("register bash tool: %w", err)
	}

	// read
	if err := read.Register(reg); err != nil {
		return nil, fmt.Errorf("register read tool: %w", err)
	}

	// write
	if err := write.Register(reg); err != nil {
		return nil, fmt.Errorf("register write tool: %w", err)
	}

	// edit
	if err := edit.Register(reg); err != nil {
		return nil, fmt.Errorf("register edit tool: %w", err)
	}

	// glob
	var globOpts []glob.Option
	if cfg.BashWorkingDir != "" {
		globOpts = append(globOpts, glob.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := glob.Register(reg, globOpts...); err != nil {
		return nil, fmt.Errorf("register glob tool: %w", err)
	}

	// grep
	var grepOpts []grep.Option
	if cfg.BashWorkingDir != "" {
		grepOpts = append(grepOpts, grep.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := grep.Register(reg, grepOpts...); err != nil {
		return nil, fmt.Errorf("register grep tool: %w", err)
	}

	return reg, nil
}

// RegisterReadOnly creates a registry with read, glob, grep only.
func RegisterReadOnly(cfg configs.ToolsConfig) (*tool.Registry, error) {
	reg := tool.NewRegistry()

	// read
	if err := read.Register(reg); err != nil {
		return nil, fmt.Errorf("register read tool: %w", err)
	}

	// glob
	var globOpts []glob.Option
	if cfg.BashWorkingDir != "" {
		globOpts = append(globOpts, glob.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := glob.Register(reg, globOpts...); err != nil {
		return nil, fmt.Errorf("register glob tool: %w", err)
	}

	// grep
	var grepOpts []grep.Option
	if cfg.BashWorkingDir != "" {
		grepOpts = append(grepOpts, grep.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := grep.Register(reg, grepOpts...); err != nil {
		return nil, fmt.Errorf("register grep tool: %w", err)
	}

	return reg, nil
}

// RegisterReviewTools creates a registry with read, glob, grep, bash.
func RegisterReviewTools(cfg configs.ToolsConfig) (*tool.Registry, error) {
	reg := tool.NewRegistry()

	// bash
	var bashOpts []bash.Option
	if cfg.BashTimeout > 0 {
		bashOpts = append(bashOpts, bash.WithTimeout(time.Duration(cfg.BashTimeout)*time.Second))
	}

	if cfg.BashWorkingDir != "" {
		bashOpts = append(bashOpts, bash.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := bash.Register(reg, bashOpts...); err != nil {
		return nil, fmt.Errorf("register bash tool: %w", err)
	}

	// read
	if err := read.Register(reg); err != nil {
		return nil, fmt.Errorf("register read tool: %w", err)
	}

	// glob
	var globOpts []glob.Option
	if cfg.BashWorkingDir != "" {
		globOpts = append(globOpts, glob.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := glob.Register(reg, globOpts...); err != nil {
		return nil, fmt.Errorf("register glob tool: %w", err)
	}

	// grep
	var grepOpts []grep.Option
	if cfg.BashWorkingDir != "" {
		grepOpts = append(grepOpts, grep.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := grep.Register(reg, grepOpts...); err != nil {
		return nil, fmt.Errorf("register grep tool: %w", err)
	}

	return reg, nil
}
