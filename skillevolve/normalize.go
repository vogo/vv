package skillevolve

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/vogo/vage/skill"
	"github.com/vogo/vv/registries"
)

const (
	maxInstructionLines = 500
	maxSkillMDBytes     = 50 * 1024
	minPrivacyRunes     = 12
	fallbackSkillName   = "skill-from-session"
)

func normalizeExtracted(in ExtractedSkill, feat SessionFeatures) (ExtractedSkill, error) {
	out := in
	out.Name = normalizeName(out.Name)
	out.Instructions = stripUserTaskLeak(out.Instructions, feat.UserTask)
	out.Description = stripUserTaskLeak(out.Description, feat.UserTask)
	out.Instructions = truncateLines(out.Instructions, maxInstructionLines)

	if out.Metadata == nil {
		out.Metadata = map[string]string{}
	}
	out.Metadata["source_session"] = feat.SessionID
	out.Metadata["extracted_at"] = time.Now().UTC().Format(time.RFC3339)
	out.Metadata["origin"] = "evolution"
	if len(out.ObservedTools) > 0 {
		out.Metadata["observed_tools"] = strings.Join(out.ObservedTools, ",")
	}

	rendered := renderSkillMarkdown(out)
	if len(rendered) > maxSkillMDBytes {
		out.Instructions = shrinkInstructionsToFit(out, maxSkillMDBytes)
		rendered = renderSkillMarkdown(out)
		if len(rendered) > maxSkillMDBytes {
			return ExtractedSkill{}, fmt.Errorf("skill markdown exceeds %d bytes after truncation", maxSkillMDBytes)
		}
	}
	stripRenderedLeaks(&out, feat.UserTask)
	return out, nil
}

func normalizeName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII && unicode.IsLower(r):
			b.WriteRune(r)
			prevHyphen = false
		case unicode.IsDigit(r):
			b.WriteRune(r)
			prevHyphen = false
		case r == '-':
			if !prevHyphen && b.Len() > 0 {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	s = strings.Trim(b.String(), "-")
	if skill.ValidateName(s) != nil {
		s = fallbackSkillName
	}
	switch s {
	case registries.SkillReview, registries.SkillResearch:
		s = s + "-evolved"
	}
	if skill.ValidateName(s) != nil {
		s = fallbackSkillName
	}
	return s
}

func longestUserTaskLeak(haystack, userTask string) string {
	if len(userTask) < minPrivacyRunes || haystack == "" {
		return ""
	}
	for n := len(userTask); n >= minPrivacyRunes; n-- {
		for i := 0; i+n <= len(userTask); i++ {
			sub := userTask[i : i+n]
			if strings.Contains(haystack, sub) {
				return sub
			}
		}
	}
	return ""
}

func stripUserTaskLeak(text, userTask string) string {
	out := text
	for {
		sub := longestUserTaskLeak(out, userTask)
		if sub == "" {
			return out
		}
		out = strings.ReplaceAll(out, sub, "")
		slog.Warn("vv: stripped user-task substring from skill field", "bytes", len(sub))
	}
}

// stripRenderedLeaks removes UserTask substrings still present in the SKILL.md
// that would be written (frontmatter Description, metadata, instructions).
func stripRenderedLeaks(out *ExtractedSkill, userTask string) {
	for {
		sub := longestUserTaskLeak(renderSkillMarkdown(*out), userTask)
		if sub == "" {
			return
		}
		changed := false
		if strings.Contains(out.Instructions, sub) {
			out.Instructions = strings.ReplaceAll(out.Instructions, sub, "")
			changed = true
		}
		if strings.Contains(out.Description, sub) {
			out.Description = strings.ReplaceAll(out.Description, sub, "")
			changed = true
		}
		for k, v := range out.Metadata {
			if strings.Contains(v, sub) {
				out.Metadata[k] = strings.ReplaceAll(v, sub, "")
				changed = true
			}
		}
		slog.Warn("vv: stripped user-task substring from rendered skill markdown", "bytes", len(sub))
		if !changed {
			return
		}
	}
}

func truncateLines(s string, max int) string {
	if max <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	kept := lines[:max]
	if max > 0 {
		kept[max-1] = strings.TrimRight(kept[max-1], "\r") + " [truncated]"
	}
	return strings.Join(kept, "\n")
}

func shrinkInstructionsToFit(s ExtractedSkill, maxBytes int) string {
	instr := s.Instructions
	for len(instr) > 0 && len(renderSkillMarkdown(ExtractedSkill{
		Name:         s.Name,
		Description:  s.Description,
		Instructions: instr,
		Metadata:     s.Metadata,
	})) > maxBytes {
		if len(instr) < 64 {
			return instr
		}
		instr = instr[:len(instr)*9/10]
		for len(instr) > 0 && instr[len(instr)-1]&0xc0 == 0x80 {
			instr = instr[:len(instr)-1]
		}
	}
	return instr
}
