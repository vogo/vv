package skillevolve

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/vogo/vage/skill"
)

// HeuristicExtractor builds a skill from the tool sequence with a fixed
// template. It is a unit-test fixture only and is not wired into Init.
type HeuristicExtractor struct{}

// NewHeuristicExtractor returns a template extractor that needs no Caller.
func NewHeuristicExtractor() *HeuristicExtractor {
	return &HeuristicExtractor{}
}

// Extract implements Extractor.
func (h *HeuristicExtractor) Extract(ctx context.Context, feat SessionFeatures) (ExtractedSkill, error) {
	if err := ctx.Err(); err != nil {
		return ExtractedSkill{}, err
	}

	name := heuristicName(feat)
	var tools []string
	seen := map[string]bool{}
	var order []string
	for _, step := range feat.ToolSequence {
		if step.Name == "" {
			continue
		}
		if !seen[step.Name] {
			seen[step.Name] = true
			tools = append(tools, step.Name)
		}
		order = append(order, step.Name)
	}

	var b strings.Builder
	b.WriteString("Reusable procedure extracted from a successful session.\n\n")
	b.WriteString("Observed tool order:\n")
	if len(order) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for i, n := range order {
			fmt.Fprintf(&b, "%d. %s\n", i+1, n)
		}
	}
	b.WriteString("\nDo not copy user text. Restate the task in generic terms.\n")

	out := ExtractedSkill{
		Name:          name,
		Description:   "Heuristic skill from observed tool sequence",
		Instructions:  b.String(),
		ObservedTools: tools,
	}
	return normalizeExtracted(out, feat)
}

func heuristicName(feat SessionFeatures) string {
	freq := map[string]int{}
	best := ""
	bestN := 0
	for _, step := range feat.ToolSequence {
		n := kebabToken(step.Name)
		if n == "" {
			continue
		}
		freq[n]++
		if freq[n] > bestN {
			bestN = freq[n]
			best = n
		}
	}
	if best == "" {
		return fallbackSkillName
	}
	name := best + "-workflow"
	if skill.ValidateName(name) != nil {
		return fallbackSkillName
	}
	return name
}

func kebabToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	hyphen := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if b.Len() > 0 && !hyphen {
			b.WriteRune('-')
			hyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}
