package tools

import (
	"fmt"
	"time"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/bashtool"
	"github.com/vogo/vage/tool/edittool"
	"github.com/vogo/vage/tool/globtool"
	"github.com/vogo/vage/tool/greptool"
	"github.com/vogo/vage/tool/readtool"
	"github.com/vogo/vage/tool/writetool"
	"github.com/vogo/vagents/vaga/config"
)

// Register creates a tool registry and registers all built-in tools.
func Register(cfg config.ToolsConfig) (*tool.Registry, error) {
	reg := tool.NewRegistry()

	// bash
	var bashOpts []bashtool.Option
	if cfg.BashTimeout > 0 {
		bashOpts = append(bashOpts, bashtool.WithTimeout(time.Duration(cfg.BashTimeout)*time.Second))
	}

	if cfg.BashWorkingDir != "" {
		bashOpts = append(bashOpts, bashtool.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := bashtool.Register(reg, bashOpts...); err != nil {
		return nil, fmt.Errorf("register bash tool: %w", err)
	}

	// read
	if err := readtool.Register(reg); err != nil {
		return nil, fmt.Errorf("register read tool: %w", err)
	}

	// write
	if err := writetool.Register(reg); err != nil {
		return nil, fmt.Errorf("register write tool: %w", err)
	}

	// edit
	if err := edittool.Register(reg); err != nil {
		return nil, fmt.Errorf("register edit tool: %w", err)
	}

	// glob
	if err := globtool.Register(reg); err != nil {
		return nil, fmt.Errorf("register glob tool: %w", err)
	}

	// grep
	if err := greptool.Register(reg); err != nil {
		return nil, fmt.Errorf("register grep tool: %w", err)
	}

	return reg, nil
}

// RegisterReadOnly creates a registry with read, glob, grep only.
func RegisterReadOnly(cfg config.ToolsConfig) (*tool.Registry, error) {
	reg := tool.NewRegistry()

	// read
	if err := readtool.Register(reg); err != nil {
		return nil, fmt.Errorf("register read tool: %w", err)
	}

	// glob
	if err := globtool.Register(reg); err != nil {
		return nil, fmt.Errorf("register glob tool: %w", err)
	}

	// grep
	if err := greptool.Register(reg); err != nil {
		return nil, fmt.Errorf("register grep tool: %w", err)
	}

	return reg, nil
}

// RegisterReviewTools creates a registry with read, glob, grep, bash.
func RegisterReviewTools(cfg config.ToolsConfig) (*tool.Registry, error) {
	reg := tool.NewRegistry()

	// bash
	var bashOpts []bashtool.Option
	if cfg.BashTimeout > 0 {
		bashOpts = append(bashOpts, bashtool.WithTimeout(time.Duration(cfg.BashTimeout)*time.Second))
	}

	if cfg.BashWorkingDir != "" {
		bashOpts = append(bashOpts, bashtool.WithWorkingDir(cfg.BashWorkingDir))
	}

	if err := bashtool.Register(reg, bashOpts...); err != nil {
		return nil, fmt.Errorf("register bash tool: %w", err)
	}

	// read
	if err := readtool.Register(reg); err != nil {
		return nil, fmt.Errorf("register read tool: %w", err)
	}

	// glob
	if err := globtool.Register(reg); err != nil {
		return nil, fmt.Errorf("register glob tool: %w", err)
	}

	// grep
	if err := greptool.Register(reg); err != nil {
		return nil, fmt.Errorf("register grep tool: %w", err)
	}

	return reg, nil
}
