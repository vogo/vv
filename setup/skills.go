package setup

import (
	"github.com/vogo/vage/skill"
	"github.com/vogo/vv/registries"
)

func applySkillOpts(opts *Options, stack *registries.SkillStack) *Options {
	if stack == nil {
		return opts
	}
	if opts == nil {
		opts = &Options{}
	}
	opts.SkillManager = stack.Manager
	opts.SkillRegistry = stack.Registry
	opts.SkillVageRegistry = stack.VageRegistry
	return opts
}

func getSkillManager(opts *Options) skill.Manager {
	if opts == nil {
		return nil
	}
	return opts.SkillManager
}

func getSkillRegistry(opts *Options) *registries.SkillRegistry {
	if opts == nil || opts.SkillRegistry == nil {
		return registries.DefaultSkills()
	}
	return opts.SkillRegistry
}
