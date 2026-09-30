package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Colours in JSX come from tokens (token("--x", fallback) or var(--x)). The
// only literal left is the white mask the 3D textures are tinted from.
var jsxColour = regexp.MustCompile("[\"'`]#[0-9a-fA-F]{3,6}[\"'`]|\\b(hsla?|rgba?)\\(")

func TestJSXTakesColoursFromTokens(t *testing.T) {
	files, err := filepath.Glob("src/*.jsx")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "token(") || strings.Contains(line, "rgba(255,255,255,") {
				continue
			}
			if jsxColour.MatchString(line) {
				t.Errorf("%s:%d: colour literal: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
