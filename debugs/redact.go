package debugs

import (
	"regexp"
	"strings"
)

var (
	apiKeyRe        = regexp.MustCompile(`(?i)("?api[_-]?key"?\s*[:=]\s*")([^"]+)(")`)
	authHeaderRe    = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*"?)(bearer\s+)?([A-Za-z0-9._\-]+)`)
	queryAPIKeyRe   = regexp.MustCompile(`(?i)([?&]api-?key=)([^&\s"]+)`)
	redactedSecrets = []string{
		"VV_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "AI_API_KEY",
	}
)

// Redact scrubs known secret-bearing patterns from a string. Intended for
// config snapshots only; prompts and tool content are NOT auto-redacted.
func Redact(s string) string {
	if s == "" {
		return s
	}

	s = apiKeyRe.ReplaceAllString(s, `${1}<redacted>${3}`)
	s = authHeaderRe.ReplaceAllString(s, `${1}${2}<redacted>`)
	s = queryAPIKeyRe.ReplaceAllString(s, `${1}<redacted>`)

	for _, name := range redactedSecrets {
		if v := getEnv(name); v != "" {
			s = strings.ReplaceAll(s, v, "<redacted>")
		}
	}

	return s
}
