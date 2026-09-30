package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Each theme token needs a light value and the same dark value twice: once for
// the OS setting and once for the explicit override (design spec §3.1).
var themeTokens = []string{
	"bg", "bg-2", "bg-3", "panel", "panel-2", "line", "line-2", "text", "text-dim", "text-soft",
	"accent", "accent-2", "accent-wash", "on-accent", "on-status", "on-warn", "ok", "warn", "danger", "info",
	"ns-0", "ns-1", "ns-2", "ns-3", "ns-4", "ns-other", "pod-neutral",
	"tint-fill", "tint-edge", "tint-box",
	"hatch-0", "hatch-1", "hatch-danger-0", "hatch-danger-1", "hatch-danger-text",
	"hatch-warn-0", "hatch-warn-1", "hatch-warn-text", "hatch-info-0", "hatch-info-1", "hatch-info-text",
	"plate", "floor", "shell", "shadow",
}

const tokensEnd = "/* ── end tokens ── */"

func stylesheet(t *testing.T) (tokens, rules string) {
	t.Helper()
	b, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	tokens, rules, ok := strings.Cut(string(b), tokensEnd)
	if !ok {
		t.Fatalf("styles.css has no %q marker after the token blocks", tokensEnd)
	}
	return tokens, rules
}

func TestStylesDefineEveryTokenForBothThemes(t *testing.T) {
	tokens, _ := stylesheet(t)
	for _, want := range []string{`@media (prefers-color-scheme: dark)`, `:root:not([data-theme="light"])`, `:root[data-theme="dark"]`} {
		if !strings.Contains(tokens, want) {
			t.Errorf("token blocks lack %s", want)
		}
	}
	for _, tok := range themeTokens {
		if n := strings.Count(tokens, "--"+tok+":"); n != 3 {
			t.Errorf("--%s is defined %d times, want 3 (light, OS dark, forced dark)", tok, n)
		}
	}
}

// Component rules use tokens only, so both themes stay complete.
var cssColour = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\b(rgba?|hsla?)\(|[:\s](white|black)\s*(;|,|\)|$)`)

func TestStylesUseTokensOnly(t *testing.T) {
	_, rules := stylesheet(t)
	for i, line := range strings.Split(rules, "\n") {
		if strings.Contains(line, "mask") { // mask images use alpha only
			continue
		}
		if cssColour.MatchString(line) {
			t.Errorf("styles.css rule line %d uses a colour literal: %s", i+1, strings.TrimSpace(line))
		}
	}
}
