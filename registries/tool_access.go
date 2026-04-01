package registries

import (
	"fmt"
	"slices"
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

// ToolCapability represents a single tool access capability.
type ToolCapability string

const (
	CapRead    ToolCapability = "read"    // read files
	CapWrite   ToolCapability = "write"   // write, edit files
	CapExecute ToolCapability = "execute" // bash commands
	CapSearch  ToolCapability = "search"  // glob, grep
)

// ToolProfile defines a named set of tool capabilities.
type ToolProfile struct {
	Name         string
	Capabilities []ToolCapability
}

// Predefined profiles matching current access patterns.
var (
	ProfileFull     = ToolProfile{"full", []ToolCapability{CapRead, CapWrite, CapExecute, CapSearch}}
	ProfileReadOnly = ToolProfile{"read-only", []ToolCapability{CapRead, CapSearch}}
	ProfileReview   = ToolProfile{"review", []ToolCapability{CapRead, CapSearch, CapExecute}}
	ProfileNone     = ToolProfile{"none", nil}
)

// Has returns true if the profile includes the given capability.
func (p ToolProfile) Has(cap ToolCapability) bool {
	return slices.Contains(p.Capabilities, cap)
}

// ProfileByName resolves a profile name string (e.g., from DynamicAgentSpec.ToolAccess JSON)
// to a ToolProfile. Returns false if the name is not recognized.
func ProfileByName(name string) (ToolProfile, bool) {
	switch name {
	case "full":
		return ProfileFull, true
	case "read-only":
		return ProfileReadOnly, true
	case "review":
		return ProfileReview, true
	case "none":
		return ProfileNone, true
	default:
		return ToolProfile{}, false
	}
}

// BuildRegistry constructs a new tool.Registry containing only the tools
// granted by this profile's capabilities. Each tool is freshly registered
// with the provided tool configuration (bash timeout, working dir, etc.).
func (p ToolProfile) BuildRegistry(toolsCfg configs.ToolsConfig) (*tool.Registry, error) {
	if len(p.Capabilities) == 0 {
		return tool.NewRegistry(), nil
	}

	reg := tool.NewRegistry()

	for _, cap := range p.Capabilities {
		if err := registerCapabilityTools(reg, cap, toolsCfg); err != nil {
			return nil, fmt.Errorf("register %s tools: %w", cap, err)
		}
	}

	return reg, nil
}

// registerCapabilityTools registers the tools for a single capability.
func registerCapabilityTools(reg *tool.Registry, cap ToolCapability, cfg configs.ToolsConfig) error {
	switch cap {
	case CapRead:
		return read.Register(reg)
	case CapWrite:
		if err := write.Register(reg); err != nil {
			return err
		}

		return edit.Register(reg)
	case CapExecute:
		var bashOpts []bash.Option
		if cfg.BashTimeout > 0 {
			bashOpts = append(bashOpts, bash.WithTimeout(time.Duration(cfg.BashTimeout)*time.Second))
		}

		if cfg.BashWorkingDir != "" {
			bashOpts = append(bashOpts, bash.WithWorkingDir(cfg.BashWorkingDir))
		}

		return bash.Register(reg, bashOpts...)
	case CapSearch:
		var globOpts []glob.Option
		if cfg.BashWorkingDir != "" {
			globOpts = append(globOpts, glob.WithWorkingDir(cfg.BashWorkingDir))
		}

		if err := glob.Register(reg, globOpts...); err != nil {
			return err
		}

		var grepOpts []grep.Option
		if cfg.BashWorkingDir != "" {
			grepOpts = append(grepOpts, grep.WithWorkingDir(cfg.BashWorkingDir))
		}

		return grep.Register(reg, grepOpts...)
	default:
		return fmt.Errorf("unknown capability: %s", cap)
	}
}
