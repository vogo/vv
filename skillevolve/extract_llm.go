package skillevolve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	largemodel "github.com/vogo/largemodel/model"
	"github.com/vogo/largemodel/schema"
)

const extractSystemHint = `You extract a reusable Agent Skill from a successful coding-agent session.
Return ONLY a JSON object with keys: name, description, instructions, observed_tools.
Rules:
- name: kebab-case, lowercase letters, digits, hyphens.
- description: one sentence.
- instructions: reusable discipline for future similar tasks. Do not copy user text verbatim into instructions. Do not restate identity or environment.
- observed_tools: array of tool names you saw, documentation only.
- Write reusable procedure, not a recap of this one session.`

// LLMExtractor asks an LLM to draft a skill as JSON.
type LLMExtractor struct {
	caller largemodel.Caller
	model  string
}

// NewLLMExtractor constructs the production extractor.
func NewLLMExtractor(caller largemodel.Caller, model string) *LLMExtractor {
	return &LLMExtractor{caller: caller, model: model}
}

type llmSkillJSON struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Instructions  string   `json:"instructions"`
	ObservedTools []string `json:"observed_tools"`
}

// Extract implements Extractor. Parse failure retries once with a repair hint.
func (e *LLMExtractor) Extract(ctx context.Context, feat SessionFeatures) (ExtractedSkill, error) {
	if e == nil || e.caller == nil {
		return ExtractedSkill{}, fmt.Errorf("llm extractor: caller is required")
	}

	prompt := buildExtractPrompt(feat)
	text, err := e.callJSON(ctx, prompt)
	if err != nil {
		return ExtractedSkill{}, err
	}
	parsed, err := parseSkillJSON(text)
	if err != nil {
		repair := prompt + "\n\nYour previous reply was not valid JSON. Return ONLY the JSON object, no markdown fences, no commentary."
		text, err2 := e.callJSON(ctx, repair)
		if err2 != nil {
			return ExtractedSkill{}, err2
		}
		parsed, err = parseSkillJSON(text)
		if err != nil {
			return ExtractedSkill{}, fmt.Errorf("parse extract json: %w", err)
		}
	}
	return normalizeExtracted(parsed, feat)
}

func (e *LLMExtractor) callJSON(ctx context.Context, user string) (string, error) {
	proto := e.caller.Protocol()
	req := &largemodel.Request{
		Model: e.model,
		Messages: []schema.Message{
			schema.NewSystemMessage(proto, extractSystemHint),
			schema.NewUserMessage(proto, user),
		},
	}
	resp, err := e.caller.Call(ctx, req)
	if err != nil {
		return "", fmt.Errorf("llm extract: %w", err)
	}
	if resp == nil || resp.Message.Text() == "" {
		return "", fmt.Errorf("llm extract: empty response")
	}
	return resp.Message.Text(), nil
}

func buildExtractPrompt(feat SessionFeatures) string {
	var b strings.Builder
	b.WriteString("User task (do not copy user text verbatim into instructions):\n")
	b.WriteString(feat.UserTask)
	b.WriteString("\n\nTool sequence:\n")
	for _, step := range feat.ToolSequence {
		fmt.Fprintf(&b, "- %s (agent=%s success=%v)\n", step.Name, step.AgentID, step.Success)
	}
	if len(feat.SpawnedSpecs) > 0 {
		b.WriteString("\nspawn_worker specs:\n")
		for _, s := range feat.SpawnedSpecs {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	if feat.LastAssistant != "" {
		b.WriteString("\nLast assistant excerpt:\n")
		b.WriteString(feat.LastAssistant)
		b.WriteByte('\n')
	}
	return b.String()
}

func parseSkillJSON(text string) (ExtractedSkill, error) {
	raw := strings.TrimSpace(text)
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	}
	var parsed llmSkillJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ExtractedSkill{}, err
	}
	return ExtractedSkill{
		Name:          parsed.Name,
		Description:   parsed.Description,
		Instructions:  parsed.Instructions,
		ObservedTools: parsed.ObservedTools,
	}, nil
}
