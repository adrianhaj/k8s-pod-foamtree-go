package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The binary must serve the whole UI itself: in-cluster there may be no
// internet, and the CSP only allows scripts from 'self'.
func TestNoExternalScripts(t *testing.T) {
	index, err := fs.ReadFile(Static, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`<script[^>]+src="(https?:)?//`).Find(index); m != nil {
		t.Fatalf("external script in index.html: %s", m)
	}
	if regexp.MustCompile(`text/babel`).Match(index) {
		t.Fatal("index.html still relies on in-browser Babel")
	}
	for _, f := range []string{"app.js", "three.js", "vendor/react.production.min.js", "vendor/react-dom.production.min.js"} {
		if b, err := fs.ReadFile(Static, f); err != nil || len(b) == 0 {
			t.Fatalf("%s missing from the embed (run `make web`): %v", f, err)
		}
	}
}

// The assistant spends tokens only on an explicit click: one module posts to
// the chat endpoint, and only assistant.jsx calls it.
func TestChatEndpointHasOneCaller(t *testing.T) {
	files, _ := filepath.Glob("src/*.jsx")
	posts := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s, name := string(b), filepath.Base(f)
		if strings.Contains(s, "/api/llm/chat") {
			posts++
			if name != "llm.jsx" {
				t.Errorf("%s posts to the chat endpoint; only llm.jsx may", name)
			}
		}
		if strings.Contains(s, "streamChat(") && !slices.Contains([]string{"llm.jsx", "assistant.jsx"}, name) {
			t.Errorf("%s calls streamChat; only assistant.jsx may", name)
		}
	}
	if posts != 1 {
		t.Fatalf("expected exactly one file posting to /api/llm/chat, got %d", posts)
	}
}
