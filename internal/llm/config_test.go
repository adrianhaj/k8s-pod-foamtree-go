package llm

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckURL(t *testing.T) {
	c := Config{URL: "https://llm.internal.example/v1", AllowedHosts: []string{"api.openai.com", "*.openai.azure.com"}}
	for raw, ok := range map[string]bool{
		"https://api.openai.com/v1":               true,
		"https://team.openai.azure.com/openai/v1": true,
		"https://llm.internal.example/v1":         true,
		"http://api.openai.com/v1":                false,
		"https://evil.example/v1":                 false,
		"https://api.openai.com.evil.example/v1":  false,
		"ftp://api.openai.com":                    false,
		"api.openai.com/v1":                       false,
		"":                                        false,
	} {
		if _, err := c.checkURL(raw); (err == nil) != ok {
			t.Errorf("%q: err=%v", raw, err)
		}
	}
	local := Config{AllowAnyURL: true}
	for _, raw := range []string{"http://localhost:11434/v1", "http://10.0.0.5:8000/v1"} {
		if _, err := local.checkURL(raw); err != nil {
			t.Errorf("loopback run should allow %q: %v", raw, err)
		}
	}
}

func TestPublicOnly(t *testing.T) {
	for addr, ok := range map[string]bool{
		"93.184.216.34:443":          true,
		"[2606:4700:4700::1111]:443": true,
		"127.0.0.1:443":              false,
		"10.1.2.3:443":               false,
		"172.16.0.1:443":             false,
		"192.168.1.1:443":            false,
		"169.254.169.254:80":         false,
		"100.100.100.200:80":         false,
		"0.0.0.0:443":                false,
		"[::1]:443":                  false,
		"[fd00::1]:443":              false,
		"[fe80::1]:443":              false,
		"[::ffff:10.0.0.1]:443":      false,
	} {
		if err := publicOnly("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("%s: err=%v", addr, err)
		}
	}
}

// End to end: an allowed hostname that resolves to a private address still fails.
func TestViewerClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, err := viewerClient().Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("dialled a loopback address: %v", err)
	}
}

func TestServerKey(t *testing.T) {
	if k, _ := (Config{Key: "env-key"}).serverKey(); k != "env-key" {
		t.Fatalf("env key: %q", k)
	}
	path := filepath.Join(t.TempDir(), "api-key")
	os.WriteFile(path, []byte("file-key\n"), 0o600)
	if k, err := (Config{KeyFile: path}).serverKey(); err != nil || k != "file-key" {
		t.Fatalf("file key: %q %v", k, err)
	}
	if _, err := (Config{KeyFile: path + ".missing"}).serverKey(); err == nil {
		t.Fatal("missing key file accepted")
	}
}
