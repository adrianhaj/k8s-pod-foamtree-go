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
		"https://user:pw@api.openai.com/v1":       false,
		"https://api.openai.com@evil.example/v1":  false,
		"https:///v1":                             false,
		"https://API.OpenAI.com/v1":               true,
		"https://TEAM.openai.azure.com/v1":        true,
		"https://LLM.Internal.Example/v1":         true,
	} {
		if _, err := c.checkURL(raw); (err == nil) != ok {
			t.Errorf("%q: err=%v", raw, err)
		}
	}
	upper := Config{URL: "https://LLM.Internal.Example/v1"}
	if _, err := upper.checkURL("https://llm.internal.example/v1"); err != nil {
		t.Errorf("operator URL host should match case-insensitively: %v", err)
	}
	local := Config{AllowAnyURL: true}
	if _, err := local.checkURL("http://user:pw@localhost:11434/v1"); err == nil {
		t.Error("userinfo accepted on a loopback run")
	}
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
		"[64:ff9b::a00:1]:443":       false,
		"[64:ff9b:1::a00:1]:443":     false,
		"[2002:a00:1::]:443":         false,
		"[::a00:1]:443":              false,
		"[64:ff9b::808:808]:443":     true,
		"0.1.2.3:443":                false,
		"240.0.0.1:443":              false,
		"198.18.0.1:443":             false,
		"198.19.255.255:443":         false,
		"192.0.0.1:443":              false,
		"[fec0::1]:443":              false,
		"224.0.0.1:443":              false,
		"[ff02::1]:443":              false,
		"[::]:443":                   false,
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

func TestViewerClientDoesNotFollowRedirects(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := viewerClient()
	c.Transport = &http.Transport{}
	resp, err := c.Post(srv.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || hit {
		t.Fatalf("followed a redirect: status=%d hit=%v", resp.StatusCode, hit)
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
