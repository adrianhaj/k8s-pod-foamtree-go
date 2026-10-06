package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// themeTokens are the console tokens the docs theme copies; they must keep the console's values.
var themeTokens = []string{"--accent", "--text", "--text-dim", "--text-soft", "--line", "--panel", "--panel-2", "--danger", "--warn", "--info"}

func tokenValues(t *testing.T, css string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})`).FindAllStringSubmatch(css, -1) {
		if _, seen := out[m[1]]; !seen {
			out[m[1]] = strings.ToLower(m[2])
		}
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDocsThemeMatchesConsole(t *testing.T) {
	console := read(t, "web/static/styles.css")
	docs := read(t, "docs/assets/css/custom.css")
	consoleLight, consoleDark, _ := strings.Cut(console, "@media (prefers-color-scheme: dark)")
	docsLight, docsDark, ok := strings.Cut(docs, "html.dark")
	if !ok {
		t.Fatal("custom.css has no html.dark block")
	}
	for name, pair := range map[string][2]string{"light": {consoleLight, docsLight}, "dark": {consoleDark, docsDark}} {
		want, got := tokenValues(t, pair[0]), tokenValues(t, pair[1])
		for _, tok := range themeTokens {
			if got[tok] != want[tok] {
				t.Errorf("%s %s: docs %q, console %q", name, tok, got[tok], want[tok])
			}
		}
	}
}
