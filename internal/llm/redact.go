package llm

import (
	"regexp"
	"strings"
)

const masked = "[REDACTED]"

// secretRules mask values that look like secrets before anything leaves for
// the model. Order matters: whole tokens first, then name=value pairs.
// ponytail: patterns, not entropy detection; a secret in an unusual shape can
// pass. Add a rule when one is found.
var secretRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END[A-Z ]*PRIVATE KEY-----|$)`), masked},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), masked},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), masked},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`), masked},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), masked},
	{regexp.MustCompile(`\b(?:sk|rk)[-_](?:live_|test_|proj-|ant-)?[A-Za-z0-9_-]{20,}`), masked},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), masked},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/-]{8,}=*`), "${1} " + masked},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), "${1}" + masked + "@"},
	{regexp.MustCompile(`(?i)((?:passw(?:or)?d|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credentials?|auth)[a-z0-9_.-]*["']?\s*[:=]\s*["']?)[^\s"',;&}]{4,}`), "${1}" + masked},
}

// redact masks likely secrets and reports how many it masked.
func redact(s string) (string, int) {
	before := strings.Count(s, masked)
	for _, r := range secretRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s, strings.Count(s, masked) - before
}

func redactMessages(msgs []Message) ([]Message, int) {
	out := make([]Message, len(msgs))
	total := 0
	for i, m := range msgs {
		var n int
		m.Content, n = redact(m.Content)
		out[i], total = m, total+n
	}
	return out, total
}
