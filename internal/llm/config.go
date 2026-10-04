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
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", fmt.Errorf("base URL must look like https://host/v1, got %q", raw)
	}
	// Rebuilt from the parts that were checked, so the string that is sent is the one that was validated.
	clean := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
	if c.AllowAnyURL {
		return clean, nil
	}
	if u.Scheme != "https" {
		return "", errors.New("base URL must use https")
	}
	host := strings.ToLower(u.Hostname())
	if own, err := url.Parse(c.URL); err == nil && c.URL != "" && strings.ToLower(own.Hostname()) == host {
		return clean, nil
	}
	for _, g := range c.AllowedHosts {
		if ok, _ := path.Match(strings.ToLower(g), host); ok {
			return clean, nil
		}
	}
	return "", fmt.Errorf("host %s is not allowed: ask the operator to add it to --llm-allowed-hosts", host)
}

// CheckBaseURL applies the shape rules every base URL must pass: http or
// https, a host, no credentials, query or fragment.
func CheckBaseURL(raw string) error {
	_, err := Config{AllowAnyURL: true}.checkURL(raw)
	return err
}

// blocked lists what netip's IsGlobalUnicast and IsPrivate let through but is
// still not a public destination.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

func isPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	if nat64.Contains(ip) {
		b := ip.As16()
		return isPublic(netip.AddrFrom4([4]byte(b[12:])))
	}
	return true
}

// publicOnly runs at dial time, after DNS, so a hostname that resolves to a
// cluster, node or metadata address is refused too.
func publicOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("refusing to dial %s: %w", address, err)
	}
	if !isPublic(ap.Addr()) {
		return fmt.Errorf("refusing to dial %s: not a public address", address)
	}
	return nil
}

// viewerClient has no proxy on purpose: the dial check must see the real
// destination, not a proxy's address. It never follows redirects: a hop would
// skip the https and allow-list checks and carry the prompt and key along.
func viewerClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	return &http.Client{
		Transport:     &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, ForceAttemptHTTP2: true},
		CheckRedirect: noRedirect,
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
