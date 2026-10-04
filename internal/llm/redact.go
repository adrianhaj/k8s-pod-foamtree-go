package llm

import (
	"regexp"
	"strings"
)

const masked = "[REDACTED]"

// A keyword may carry a separator-led suffix (DB_PASSWORD_FILE, token_count) but
// not a letter-led one, so tokens, tokenizer and passwordless stay readable.
// pass, sig and auth are short enough to need a non-letter in front and no suffix.
const (
	keyword = `(?:passw(?:or)?d|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|account[_-]?key|credentials?|signature)(?:[_.-][a-z0-9_.-]*)?`
	edge    = `(?:^|[^a-z])(?:pass|sig|auth(?:orization)?)`
	// These name a secret outright: any value of 4+ characters is masked.
	plain  = `(?im)((?:passw(?:or)?d|pwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|account[_-]?key|(?:^|[^a-z])pass)\\?["']?[ \t]*[:=][ \t]*)([^\s"',;&}\\]{4,})`
	assign = `(?im)((?:` + keyword + `|` + edge + `)\\?["']?[ \t]*[:=][ \t]*`
)

type rule struct {
	re   *regexp.Regexp
	repl string
	ok   func(last string) bool // judges the last capture group; nil accepts every match
}

var (
	letters   = regexp.MustCompile(`^[A-Za-z]+$`)
	digits    = regexp.MustCompile(`^[0-9]+$`)
	plainWord = regexp.MustCompile(`^[A-Z]?[a-z]+$`)
)

var notValues = map[string]bool{"true": true, "false": true, "null": true, "none": true, "nil": true, "empty": true, "unset": true}

func isSecretValue(v string) bool { return v != masked && !notValues[strings.ToLower(v)] }
func isValue(v string) bool       { return v != masked }
func looksSecret(v string) bool {
	return v != masked && !letters.MatchString(v) && !digits.MatchString(v)
}
func notWord(v string) bool { return !plainWord.MatchString(v) }
func realUserinfo(v string) bool {
	return strings.Trim(strings.ReplaceAll(v, masked, ""), ":") != ""
}

// secretRules mask values that look like secrets before anything leaves for
// the model. Order matters: the Authorization header first so it counts once,
// then whole tokens, then name=value pairs.
// ponytail: patterns, not entropy detection; a secret in an unusual shape can
// pass, and after token, auth and credential keys a bare value of only letters
// or only digits is read as a word or a number. Add a rule when one is found.
var secretRules = []rule{
	{regexp.MustCompile(`(?i)\b(authorization:[ \t]*)([^\r\n]+)`), "${1}" + masked, isValue},
	{regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END[A-Z ]*PRIVATE KEY-----|$)`), masked, nil},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), masked, nil},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), masked, nil},
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`), masked, nil},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), masked, nil},
	{regexp.MustCompile(`\b(?:sk|rk)[-_](?:live_|test_|proj-|ant-)?[A-Za-z0-9_-]{20,}`), masked, nil},
	{regexp.MustCompile(`\b(?:glpat-[A-Za-z0-9_-]{20,}|hf_[A-Za-z0-9]{30,}|npm_[A-Za-z0-9]{30,})`), masked, nil},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), masked, nil},
	{regexp.MustCompile(`(?i)\b(bearer|basic)[ \t]+([A-Za-z0-9._~+/-]{8,}=*)`), "${1} " + masked, notWord},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s:@]*:[^/\s@]+)@`), "${1}" + masked + "@", realUserinfo},
	{regexp.MustCompile(`(?i)(--(?:password|passwd|pwd|secret|token|api-?key|access-?key|private-?key)(?:=|[ \t]+))([^\s"'\\,;&}-][^\s"'\\,;&}]{5,})`), "${1}" + masked, isValue},
	{regexp.MustCompile(plain), "${1}" + masked, isSecretValue},
	{regexp.MustCompile(assign + `\\?")([^"\\\n]{4,})`), "${1}" + masked, isValue},
	{regexp.MustCompile(assign + `')([^'\n]{4,})`), "${1}" + masked, isValue},
	{regexp.MustCompile(assign + `)([^\s"',;&}\\]{6,})`), "${1}" + masked, looksSecret},
}

func (r rule) apply(s string) (string, int) {
	var b strings.Builder
	n, last := 0, 0
	for _, loc := range r.re.FindAllStringSubmatchIndex(s, -1) {
		if r.ok != nil {
			if k := len(loc) - 2; loc[k] < 0 || !r.ok(s[loc[k]:loc[k+1]]) {
				continue
			}
		}
		b.WriteString(s[last:loc[0]])
		b.Write(r.re.ExpandString(nil, r.repl, s, loc))
		last = loc[1]
		n++
	}
	b.WriteString(s[last:])
	return b.String(), n
}

// redact masks likely secrets and reports how many it masked.
func redact(s string) (string, int) {
	total := 0
	for _, r := range secretRules {
		var n int
		s, n = r.apply(s)
		total += n
	}
	return s, total
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
