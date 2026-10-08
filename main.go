// Command k8sfoams serves a read-only dashboard of where a cluster's
// requested CPU and memory go.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/auth"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/llm"
	"github.com/adrianhaj/k8s-pod-foamtree-go/web"
)

// Set by the Makefile and Dockerfile with -ldflags "-X main.version=...".
var version = "dev"

type options struct {
	version              bool
	host                 string
	port                 int
	inCluster            bool
	authMode             string
	allowUnauthenticated bool
	oidc                 auth.Config
	emails, groups       string
	scopes, sessionKey   string
	syntheticSpec        string
	synthetic            *syntheticSource
	llm                  llm.Config
	llmHosts             string
	audit                foam.Audit
	// "" when metrics are off: no listener and no /metrics route anywhere.
	metricsAddr string
	// nil without --prices: no node or pod carries a cost.
	prices foam.Prices
	// Both empty unless the throttled rule has a Prometheus to ask.
	prometheus, prometheusContext string
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("k8sfoams", flag.ContinueOnError)
	fs.StringVar(&o.host, "host", "127.0.0.1", "address to listen on")
	fs.IntVar(&o.port, "port", 8080, "port to listen on")
	fs.BoolVar(&o.inCluster, "in-cluster", false, "use the pod's service account instead of kubeconfig")
	fs.StringVar(&o.authMode, "auth", "none", "none or oidc")
	fs.BoolVar(&o.allowUnauthenticated, "allow-unauthenticated", false, "allow --auth=none on a non-loopback address")
	fs.StringVar(&o.oidc.Issuer, "oidc-issuer", "", "OIDC issuer URL")
	fs.StringVar(&o.oidc.ClientID, "oidc-client-id", "", "OIDC client id")
	fs.StringVar(&o.oidc.RedirectURL, "oidc-redirect-url", "", "public URL of /auth/callback")
	fs.StringVar(&o.oidc.GroupsClaim, "oidc-groups-claim", "groups", "ID token claim holding group names")
	fs.StringVar(&o.scopes, "oidc-scopes", "openid,email,profile", "comma-separated scopes")
	fs.StringVar(&o.emails, "oidc-allowed-emails", "", "comma-separated emails or globs like *@example.com")
	fs.StringVar(&o.groups, "oidc-allowed-groups", "", "comma-separated groups")
	fs.StringVar(&o.syntheticSpec, "synthetic", "", "serve a made-up cluster instead, e.g. 100x50 (nodes x pods per node)")
	fs.StringVar(&o.llm.URL, "llm-url", "", "OpenAI-compatible base URL for the server's assistant connection, e.g. https://api.openai.com/v1")
	fs.StringVar(&o.llm.Model, "llm-model", "", "model for the server's assistant connection")
	fs.StringVar(&o.llm.KeyFile, "llm-api-key-file", "", "file holding the server's API key, e.g. a mounted Secret; re-read on every request")
	fs.StringVar(&o.llmHosts, "llm-allowed-hosts", "", "comma-separated host globs viewers may send their own key to, e.g. api.openai.com,*.openai.azure.com")
	fs.IntVar(&o.llm.MaxTokens, "llm-max-tokens-per-question", 50000, "token cap for one question on the server connection; 0 means none")
	var monolith, ratio, minShare float64
	var disable string
	d := foam.DefaultAudit()
	fs.Float64Var(&monolith, "audit-monolith", d.MonolithShare*100, "flag pods reserving more than this percent of their node's CPU or memory")
	fs.Float64Var(&ratio, "audit-ratio", d.RatioFactor, "flag pods whose CPU and memory shares of their node differ by this factor or more")
	fs.Float64Var(&minShare, "audit-ratio-min-share", d.RatioMinShare*100, "skip ratio-asymmetry when the pod's larger share is under this percent")
	fs.StringVar(&disable, "audit-disable", "", "comma-separated audit rules to turn off")
	fs.StringVar(&o.metricsAddr, "metrics-addr", "", "serve Prometheus metrics at /metrics on this host:port, e.g. :9090; unset means no metrics")
	var pricesFile string
	fs.StringVar(&pricesFile, "prices", "", "CSV of node prices (instance_type,region,capacity_type,hourly_usd) for cost estimates")
	fs.StringVar(&o.prometheus, "prometheus-url", "", "Prometheus base URL scraping cAdvisor, e.g. http://prometheus:9090; enables the throttled audit rule")
	fs.StringVar(&o.prometheusContext, "prometheus-context", "", "kubeconfig context that --prometheus-url watches (in-cluster when running in a pod)")
	fs.BoolVar(&o.version, "version", false, "print the version and exit")
	fs.BoolVar(&o.version, "v", false, "print the version and exit")
	if err := fs.Parse(args); err != nil || o.version {
		return o, err
	}
	// Secrets come from the environment only: flags show up in `ps`.
	o.oidc.ClientSecret = os.Getenv("K8SFOAMS_OIDC_CLIENT_SECRET")
	o.sessionKey = os.Getenv("K8SFOAMS_SESSION_KEY")
	o.llm.Key = os.Getenv("K8SFOAMS_LLM_API_KEY")
	o.llm.AllowedHosts = list(o.llmHosts)
	switch {
	case (o.llm.URL == "") != (o.llm.Model == ""):
		return o, errors.New("--llm-url and --llm-model go together")
	case o.llm.Key != "" && o.llm.KeyFile != "":
		return o, errors.New("set K8SFOAMS_LLM_API_KEY or --llm-api-key-file, not both")
	case o.llm.MaxTokens < 0:
		return o, errors.New("--llm-max-tokens-per-question must be 0 or more")
	case (o.prometheus == "") != (o.prometheusContext == ""):
		return o, errors.New("--prometheus-url and --prometheus-context go together")
	}
	if o.llm.URL != "" {
		if err := llm.CheckBaseURL(o.llm.URL); err != nil {
			return o, fmt.Errorf("--llm-url: %w", err)
		}
	}
	if err := checkMetricsAddr(o.metricsAddr, o.port); err != nil {
		return o, err
	}
	o.oidc.Scopes, o.oidc.AllowedEmails, o.oidc.AllowedGroups = list(o.scopes), list(o.emails), list(o.groups)
	a, err := auditConfig(monolith, ratio, minShare, list(disable))
	if err != nil {
		return o, err
	}
	o.audit = a
	if pricesFile != "" {
		if o.prices, err = loadPrices(pricesFile); err != nil {
			return o, fmt.Errorf("--prices: %w", err)
		}
	}
	if o.syntheticSpec != "" {
		s, err := parseSynthetic(o.syntheticSpec)
		if err != nil {
			return o, err
		}
		o.synthetic = s
	}

	switch o.authMode {
	case "none":
		if !isLoopback(o.host) && !o.allowUnauthenticated {
			return o, fmt.Errorf("refusing to serve cluster data without auth on %s: use --auth=oidc or --allow-unauthenticated", o.host)
		}
	case "oidc":
	default:
		return o, fmt.Errorf("--auth must be none or oidc, got %q", o.authMode)
	}
	// On a loopback run without auth the viewer is the operator, so a local
	// endpoint such as Ollama on localhost is fine.
	o.llm.AllowAnyURL = o.authMode == "none" && isLoopback(o.host)
	return o, nil
}

func loadPrices(name string) (foam.Prices, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return foam.ParsePrices(f)
}

// throttledSource marks the pods Prometheus reports as throttled, for the one
// context it watches. A failed query leaves every pod unmarked.
type throttledSource struct {
	source
	throttle *kube.Throttle
	context  string
}

func (s throttledSource) Snapshot(ctx context.Context, name string) ([]foam.Node, []foam.Pod, error) {
	nodes, pods, err := s.source.Snapshot(ctx, name)
	if err != nil || !s.watches(name) {
		return nodes, pods, err
	}
	shares, qerr := s.throttle.Shares(ctx)
	if qerr != nil {
		slog.Warn("throttle", "err", qerr)
	}
	for i := range pods {
		pods[i].Throttled = shares[pods[i].Namespace+"/"+pods[i].Name]
	}
	return nodes, pods, nil
}

// watches reports whether name, "" meaning the active context, is the one Prometheus watches.
func (s throttledSource) watches(name string) bool {
	if name != "" {
		return name == s.context
	}
	contexts, err := s.Contexts()
	if err != nil {
		return false
	}
	i := slices.IndexFunc(contexts, func(c kube.Context) bool { return c.Active })
	return i >= 0 && contexts[i].Context == s.context
}

// pricedSource stamps each node's price on every snapshot, so the map, the
// reports read the same costs.
type pricedSource struct {
	source
	prices foam.Prices
}

func (s pricedSource) Snapshot(ctx context.Context, name string) ([]foam.Node, []foam.Pod, error) {
	nodes, pods, err := s.source.Snapshot(ctx, name)
	// Nodes share a few label combinations: scan the table once for each.
	seen := map[[3]string]*float64{}
	for i, n := range nodes {
		k := [3]string{n.InstanceType, n.Region, n.CapacityType}
		p, ok := seen[k]
		if !ok {
			if v, found := s.prices.Price(n); found {
				p = &v
			}
			seen[k] = p
		}
		nodes[i].HourlyPrice = p
	}
	return nodes, pods, err
}

func list(s string) []string {
	var out []string
	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Range checks are written as !(ok) so NaN, which fails every comparison, is refused.
func auditConfig(monolith, ratio, minShare float64, disabled []string) (foam.Audit, error) {
	switch {
	case !(monolith > 0 && monolith <= 100):
		return foam.Audit{}, fmt.Errorf("--audit-monolith must be a percent in (0, 100], got %v", monolith)
	case !(ratio > 1 && ratio < math.Inf(1)):
		return foam.Audit{}, fmt.Errorf("--audit-ratio must be a finite factor above 1, got %v", ratio)
	case !(minShare >= 0 && minShare <= 100):
		return foam.Audit{}, fmt.Errorf("--audit-ratio-min-share must be a percent in [0, 100], got %v", minShare)
	}
	a := foam.Audit{MonolithShare: monolith / 100, RatioFactor: ratio, RatioMinShare: minShare / 100, Disabled: map[string]bool{}}
	for _, r := range disabled {
		if !slices.Contains(foam.AuditRules, r) {
			return foam.Audit{}, fmt.Errorf("--audit-disable: unknown rule %q, use %s", r, strings.Join(foam.AuditRules, ", "))
		}
		a.Disabled[r] = true
	}
	return a, nil
}

// checkMetricsAddr refuses what would only fail at bind time, or worse bind:
// a named or zero port picks something no scrape config expects.
func checkMetricsAddr(addr string, dashboardPort int) error {
	if addr == "" {
		return nil
	}
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--metrics-addr wants host:port, e.g. :9090: %w", err)
	}
	port, err := strconv.Atoi(p)
	switch {
	case err != nil || port < 1 || port > 65535:
		return fmt.Errorf("--metrics-addr port must be 1-65535, got %q", p)
	case port == dashboardPort:
		return fmt.Errorf("--metrics-addr must not use the dashboard's port %d", dashboardPort)
	}
	return nil
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// loopbackHostOnly refuses requests addressed to any other name, so a page
// that rebinds its DNS to 127.0.0.1 cannot read cluster data.
func loopbackHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower((&url.URL{Host: r.Host}).Hostname())
		if !isLoopback(host) {
			http.Error(w, "unexpected Host header", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sessionKey(encoded string) ([]byte, error) {
	if encoded == "" {
		slog.Warn("K8SFOAMS_SESSION_KEY unset: using a random key, sessions end on restart")
		key := make([]byte, 32)
		rand.Read(key)
		return key, nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("K8SFOAMS_SESSION_KEY must be 32 bytes, base64-encoded (openssl rand -base64 32)")
	}
	return key, nil
}

func run(ctx context.Context, o options) error {
	var a *auth.Auth
	if o.authMode == "oidc" {
		key, err := sessionKey(o.sessionKey)
		if err != nil {
			return err
		}
		o.oidc.SessionKey = key
		if a, err = auth.New(ctx, o.oidc); err != nil {
			return err
		}
	}
	var src source = kube.NewSource(o.inCluster)
	if o.synthetic != nil {
		src = o.synthetic
	}
	if o.prices != nil {
		src = pricedSource{src, o.prices}
	}
	if o.prometheus != "" && !o.audit.Disabled["throttled"] {
		src = throttledSource{src, &kube.Throttle{URL: o.prometheus}, o.prometheusContext}
	} else if o.synthetic == nil {
		// Nothing sets Throttled without Prometheus: say the rule is off rather than clean.
		o.audit.Disabled["throttled"] = true
	}
	assistant := llm.New(o.llm)
	assistant.Tools = func(cluster string) llm.Toolbox { return &clusterTools{src: src, cluster: cluster, audit: o.audit} }
	h := newHandler(src, web.Static, a, assistant, o.audit)
	if a == nil && isLoopback(o.host) {
		h = loopbackHostOnly(h)
	}
	// Bind every port before serving any, so a taken metrics port fails
	// startup instead of leaving a dashboard whose alerts never fire.
	endpoints := []endpoint{{name: "dashboard", addr: net.JoinHostPort(o.host, strconv.Itoa(o.port)), handler: h}}
	if o.metricsAddr != "" {
		m := newMetricsHandler(src, o.audit)
		if host, _, _ := net.SplitHostPort(o.metricsAddr); isLoopback(host) {
			m = loopbackHostOnly(m)
		}
		endpoints = append(endpoints, endpoint{name: "metrics", addr: o.metricsAddr, path: "/metrics", handler: m})
	}
	for i := range endpoints {
		e := &endpoints[i]
		ln, err := net.Listen("tcp", e.addr)
		if err != nil {
			for _, b := range endpoints[:i] {
				b.ln.Close()
			}
			return fmt.Errorf("%s: %w", e.name, err)
		}
		e.ln, e.srv = ln, &http.Server{Handler: e.handler, ReadHeaderTimeout: 10 * time.Second}
	}
	if o.metricsAddr != "" {
		go startDefault(ctx, src, 30*time.Second)
	}
	slog.Info("k8sfoams starting", "auth", o.authMode, "inCluster", o.inCluster)
	errs := make(chan error, len(endpoints))
	for _, e := range endpoints {
		slog.Info("listening", "on", e.name, "url", "http://"+e.ln.Addr().String()+e.path)
		go func() { errs <- e.srv.Serve(e.ln) }()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errs:
		// Serve only returns early on a failed accept: take the others down too.
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, e := range endpoints {
		_ = e.srv.Shutdown(shutdown)
	}
	return err
}

// endpoint is one port k8sfoams serves; ln and srv are set once bound.
type endpoint struct {
	name, addr, path string
	handler          http.Handler
	ln               net.Listener
	srv              *http.Server
}

var errNoCurrentContext = errors.New("kubeconfig has no current context")

// startDefault keeps the current context's watch started for as long as the
// server runs: /metrics reads only started contexts, and in-cluster nobody
// may open the dashboard to start one. Every retry it starts the current
// context if it is not yet, so a failed start or a `kubectl config
// use-context` catches up. Until then the scrape reports it down. An error
// is logged when it changes, not every retry.
func startDefault(ctx context.Context, src source, retry time.Duration) {
	var last string
	for {
		err := startCurrent(ctx, src)
		switch {
		case ctx.Err() != nil:
			return
		case err == nil:
			last = ""
		case err.Error() != last:
			last = err.Error()
			slog.Warn("metrics: current context not started, retrying", "every", retry, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

func startCurrent(ctx context.Context, src source) error {
	contexts, err := src.Contexts()
	if err != nil {
		return err
	}
	for _, c := range contexts {
		if !c.Active {
			continue
		}
		if slices.Contains(src.Started(), c.Context) {
			return nil
		}
		_, _, err := src.Snapshot(ctx, c.Context)
		return err
	}
	return errNoCurrentContext
}

func main() {
	o, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		slog.Error(err.Error())
		os.Exit(2)
	}
	if o.version {
		fmt.Println(version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, o); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
