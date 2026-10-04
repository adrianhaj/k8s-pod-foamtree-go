// Package llm relays a chat to an OpenAI-compatible API. A viewer may bring
// their own URL and key; the operator's key only ever goes to the operator's URL.
package llm

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	URL, Model string
	// Key comes from K8SFOAMS_LLM_API_KEY. KeyFile is re-read on every request,
	// so a rotated Secret applies without a restart.
	Key, KeyFile string
	// MaxTokens caps one question on the server connection; 0 means no cap.
	MaxTokens int
	// AllowedHosts are path.Match globs a viewer may send their own key to.
	AllowedHosts []string
	// AllowAnyURL is for a loopback run without auth, where the viewer is the operator.
	AllowAnyURL bool
}

func (c Config) serverKey() (string, error) {
	if c.KeyFile == "" {
		return c.Key, nil
	}
	b, err := os.ReadFile(c.KeyFile)
	return strings.TrimSpace(string(b)), err
}

func (c Config) checkURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("base URL must look like https://host/v1, got %q", raw)
	}
	if c.AllowAnyURL {
		return u.String(), nil
	}
	if u.Scheme != "https" {
		return "", errors.New("base URL must use https")
	}
	host := u.Hostname()
	if own, err := url.Parse(c.URL); err == nil && c.URL != "" && own.Hostname() == host {
		return u.String(), nil
	}
	for _, g := range c.AllowedHosts {
		if ok, _ := path.Match(g, host); ok {
			return u.String(), nil
		}
	}
	return "", fmt.Errorf("host %s is not allowed: ask the operator to add it to --llm-allowed-hosts", host)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicOnly runs at dial time, after DNS, so a hostname that resolves to a
// cluster, node or metadata address is refused too.
func publicOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("refusing to dial %s: %w", address, err)
	}
	ip := ap.Addr().Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || cgnat.Contains(ip) {
		return fmt.Errorf("refusing to dial %s: not a public address", address)
	}
	return nil
}

// viewerClient has no proxy on purpose: the dial check must see the real
// destination, not a proxy's address.
func viewerClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	return &http.Client{Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true}}
}
