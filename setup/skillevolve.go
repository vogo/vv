package setup

import (
	"fmt"
	"strings"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/sessionlogs"
	"github.com/vogo/vv/skillevolve"
)

func installSkillEvolution(cfg *configs.Config, opts *Options, skillStack *registries.SkillStack, llm largemodel.Caller) (*skillevolve.Engine, error) {
	if cfg == nil || !cfg.SkillEvolution.IsEnabled() {
		return nil, nil
	}
	if !cfg.Session.IsEnabled() {
		return nil, fmt.Errorf("skill_evolution.enabled requires session.enabled")
	}
	skillDir := strings.TrimSpace(cfg.Agents.SkillDir)
	if skillDir == "" {
		return nil, fmt.Errorf("skill_evolution.enabled requires agents.skill_dir")
	}

	var logs *sessionlogs.Store
	if opts != nil {
		logs, _ = opts.IterationStore.(*sessionlogs.Store)
	}
	if logs == nil {
		return nil, fmt.Errorf("skill_evolution.enabled requires session transcript store")
	}
	if llm == nil {
		return nil, fmt.Errorf("skill_evolution.enabled requires an LLM caller")
	}
	if skillStack == nil || skillStack.Registry == nil || skillStack.VageRegistry == nil {
		return nil, fmt.Errorf("skill_evolution.enabled requires a skill stack")
	}

	minTurns := cfg.SkillEvolution.MinTurns
	if minTurns == 0 {
		minTurns = configs.DefaultSkillEvolutionMinTurns
	}
	minSuccess := cfg.SkillEvolution.MinToolSuccessRate
	if minSuccess == 0 {
		minSuccess = configs.DefaultSkillEvolutionMinToolSuccessRate
	}
	sim := cfg.SkillEvolution.SimilarityThreshold
	if sim == 0 {
		sim = configs.DefaultSkillEvolutionSimilarity
	}

	var store = getVectorStore(opts)
	var emb = getVectorEmbedder(opts)

	var refresher skillevolve.SchemaRefresher
	if opts != nil {
		refresher = opts.SkillSchemaRefresher
	}

	return &skillevolve.Engine{
		Logs:       logs,
		Extractor:  skillevolve.NewLLMExtractor(llm, cfg.LLM.Model),
		Deduper:    skillevolve.NewDeduper(store, emb, float32(sim)),
		Queue:      skillevolve.NewFileQueue(skillDir),
		Registrar: &skillevolve.Registrar{
			SkillDir:  skillDir,
			VV:        skillStack.Registry,
			Vage:      skillStack.VageRegistry,
			Refresher: refresher,
			Store:     store,
			Embedder:  emb,
		},
		Hooks:      getHookManager(opts),
		MinTurns:   minTurns,
		MinSuccess: minSuccess,
		Vage:       skillStack.VageRegistry,
	}, nil
}

func skillEvolveFrom(r *Result) *skillevolve.Engine {
	if r == nil {
		return nil
	}
	return r.SkillEvolve
}
