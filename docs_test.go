package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
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
	consoleLight, consoleDark, found := strings.Cut(console, "@media (prefers-color-scheme: dark)")
	if !found {
		t.Fatal("console styles.css has no dark media block")
	}
	docsLight, docsDark, ok := strings.Cut(docs, "html.dark")
	if !ok {
		t.Fatal("custom.css has no html.dark block")
	}
	for name, pair := range map[string][2]string{"light": {consoleLight, docsLight}, "dark": {consoleDark, docsDark}} {
		want, got := tokenValues(t, pair[0]), tokenValues(t, pair[1])
		for _, tok := range themeTokens {
			if want[tok] == "" {
				t.Errorf("console has no %s in %s", tok, name)
				continue
			}
			if got[tok] != want[tok] {
				t.Errorf("%s %s: docs %q, console %q", name, tok, got[tok], want[tok])
			}
		}
	}
}

func TestDocsReferenceCoversFlags(t *testing.T) {
	src := read(t, "main.go")
	page := read(t, "docs/content/docs/reference/flags.md")
	names := regexp.MustCompile(`fs\.\w+Var\(&[^,]+, "([a-z0-9-]+)"`).FindAllStringSubmatch(src, -1)
	if len(names) < 20 {
		t.Fatalf("found only %d flags in main.go; has parseFlags moved?", len(names))
	}
	for _, m := range names {
		flag := "--" + m[1]
		if m[1] == "v" {
			flag = "-v"
		}
		if !strings.Contains(page, "| `"+flag+"` |") {
			t.Errorf("flags.md has no row for %s", flag)
		}
	}
}

func TestDocsReferenceCoversAuditRules(t *testing.T) {
	page := read(t, "docs/content/docs/reference/audit-rules.md")
	for _, rule := range foam.AuditRules {
		if !strings.Contains(page, "`"+rule+"`") {
			t.Errorf("audit-rules.md has no row for %s", rule)
		}
	}
}

// The README points into the docs site instead of repeating it.
func TestReadmeLinksTheDocs(t *testing.T) {
	readme := read(t, "README.md")
	for _, want := range []string{
		"https://adrianhaj.github.io/k8s-pod-foamtree-go/",
		"https://adrianhaj.github.io/k8s-pod-foamtree-go/docs/reference/flags/",
		"https://adrianhaj.github.io/k8s-pod-foamtree-go/docs/reference/http-api/",
		"## Development",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README is missing %q", want)
		}
	}
	for _, page := range []string{"flags", "http-api"} {
		if _, err := os.Stat("docs/content/docs/reference/" + page + ".md"); err != nil {
			t.Errorf("README links the %s page: %v", page, err)
		}
	}
	if n := strings.Count(readme, "\n"); n > 60 {
		t.Errorf("README is %d lines; the details belong on the docs site", n)
	}
}
