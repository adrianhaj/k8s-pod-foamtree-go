package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
)

func TestMetricsExposition(t *testing.T) {
	memLimit := int64(2_000_000_000)
	src := &fakeSource{
		started: []string{"kind"},
		nodes: []foam.Node{
			{Name: "n1", CPU: 4000, Memory: 8_000_000_000, Pool: "general", Zone: "a", Conditions: map[string]bool{"Ready": true}},
			{Name: "n2", CPU: 2000, Memory: 4_000_000_000, Pool: "general", Zone: "a", Unschedulable: true},
			{Name: "n3", CPU: 8000, Memory: 16_000_000_000, Zone: "b", Conditions: map[string]bool{"Ready": false}},
		},
		pods: []foam.Pod{
			{Name: "web", Namespace: "shop", NodeName: "n1", CPU: 1000, Memory: 2_000_000_000,
				Containers: []foam.Container{{Name: "web", CPU: 1000, Memory: 2_000_000_000, MemoryLimit: &memLimit}}},
			{Name: "batch", Namespace: "jobs", NodeName: "n3", CPU: 500,
				Containers: []foam.Container{{Name: "batch", CPU: 500}}},
			{Name: "queued", Namespace: "jobs", CPU: 100, Memory: 100,
				Containers: []foam.Container{{Name: "queued", CPU: 100, Memory: 100}}},
		},
	}
	a := foam.DefaultAudit()
	a.Disabled["image-pull"] = true
	w := get(newMetricsHandler(src, a), "/metrics")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Content-Type"))
	}
	want := `# HELP k8sfoams_context_up 1 when the context's watch cache answered this scrape, 0 when it failed or the current context has not started.
# TYPE k8sfoams_context_up gauge
k8sfoams_context_up{context="kind"} 1
# HELP k8sfoams_capacity_cpu_cores Node CPU capacity per pool and zone.
# TYPE k8sfoams_capacity_cpu_cores gauge
k8sfoams_capacity_cpu_cores{context="kind",pool="",zone="b"} 8
k8sfoams_capacity_cpu_cores{context="kind",pool="general",zone="a"} 6
# HELP k8sfoams_headroom_cpu_cores Node CPU capacity no pod requests, per pool and zone: the empty foam.
# TYPE k8sfoams_headroom_cpu_cores gauge
k8sfoams_headroom_cpu_cores{context="kind",pool="",zone="b"} 7.5
k8sfoams_headroom_cpu_cores{context="kind",pool="general",zone="a"} 5
# HELP k8sfoams_capacity_memory_bytes Node memory capacity per pool and zone.
# TYPE k8sfoams_capacity_memory_bytes gauge
k8sfoams_capacity_memory_bytes{context="kind",pool="",zone="b"} 16000000000
k8sfoams_capacity_memory_bytes{context="kind",pool="general",zone="a"} 12000000000
# HELP k8sfoams_headroom_memory_bytes Node memory capacity no pod requests, per pool and zone: the empty foam.
# TYPE k8sfoams_headroom_memory_bytes gauge
k8sfoams_headroom_memory_bytes{context="kind",pool="",zone="b"} 16000000000
k8sfoams_headroom_memory_bytes{context="kind",pool="general",zone="a"} 10000000000
# HELP k8sfoams_requested_ratio Requested share of node capacity per pool and zone, 0.8 = 80%.
# TYPE k8sfoams_requested_ratio gauge
k8sfoams_requested_ratio{context="kind",pool="",zone="b",resource="cpu"} 0.0625
k8sfoams_requested_ratio{context="kind",pool="",zone="b",resource="memory"} 0
k8sfoams_requested_ratio{context="kind",pool="general",zone="a",resource="cpu"} 0.16666666666666666
k8sfoams_requested_ratio{context="kind",pool="general",zone="a",resource="memory"} 0.16666666666666666
# HELP k8sfoams_audit_findings Pods with each audit finding; rules turned off with --audit-disable are left out.
# TYPE k8sfoams_audit_findings gauge
k8sfoams_audit_findings{context="kind",rule="missing-requests"} 1
k8sfoams_audit_findings{context="kind",rule="missing-limits"} 2
k8sfoams_audit_findings{context="kind",rule="monolith"} 0
k8sfoams_audit_findings{context="kind",rule="ratio-asymmetry"} 0
k8sfoams_audit_findings{context="kind",rule="crashloop"} 0
k8sfoams_audit_findings{context="kind",rule="oom-killed"} 0
k8sfoams_audit_findings{context="kind",rule="throttled"} 0
k8sfoams_audit_findings{context="kind",rule="resize-deferred"} 0
k8sfoams_audit_findings{context="kind",rule="resize-infeasible"} 0
# HELP k8sfoams_node_warnings Nodes with each warning.
# TYPE k8sfoams_node_warnings gauge
k8sfoams_node_warnings{context="kind",warning="cordoned"} 1
k8sfoams_node_warnings{context="kind",warning="not-ready"} 1
k8sfoams_node_warnings{context="kind",warning="memory-pressure"} 0
k8sfoams_node_warnings{context="kind",warning="disk-pressure"} 0
k8sfoams_node_warnings{context="kind",warning="pid-pressure"} 0
k8sfoams_node_warnings{context="kind",warning="tainted"} 0
# HELP k8sfoams_pending_pods Pods no node has taken yet.
# TYPE k8sfoams_pending_pods gauge
k8sfoams_pending_pods{context="kind"} 1
`
	if got := w.Body.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// samples drops the HELP and TYPE lines.
func samples(body string) []string {
	var out []string
	for l := range strings.Lines(body) {
		if !strings.HasPrefix(l, "#") {
			out = append(out, strings.TrimSuffix(l, "\n"))
		}
	}
	return out
}

// A default context that has not started reads as down, not as a quiet
// cluster: zero findings and zero pending would look healthy.
func TestMetricsReportTheDefaultContextDownUntilStarted(t *testing.T) {
	w := get(newMetricsHandler(&fakeSource{}, foam.DefaultAudit()), "/metrics")
	if got := samples(w.Body.String()); w.Code != 200 || !slices.Equal(got, []string{`k8sfoams_context_up{context="kind"} 0`}) {
		t.Fatalf("%d %q", w.Code, got)
	}
	if !strings.Contains(w.Body.String(), "# TYPE k8sfoams_pending_pods gauge") {
		t.Fatalf("families missing:\n%s", w.Body)
	}
}

// A context that fails reads as down, with none of its numbers.
func TestMetricsReportAFailingContextDown(t *testing.T) {
	src := &fakeSource{started: []string{"kind"}, err: errors.New("gone")}
	w := get(newMetricsHandler(src, foam.DefaultAudit()), "/metrics")
	if got := samples(w.Body.String()); w.Code != 200 || !slices.Equal(got, []string{`k8sfoams_context_up{context="kind"} 0`}) {
		t.Fatalf("%d %q", w.Code, got)
	}
}

// Context names come from kubeconfig and can hold anything.
func TestMetricsEscapesLabelValues(t *testing.T) {
	name := "a\"b\\c\nd"
	src := &fakeSource{started: []string{name}, contexts: []kube.Context{{Context: name, Active: true}}}
	w := get(newMetricsHandler(src, foam.DefaultAudit()), "/metrics")
	if !strings.Contains(w.Body.String(), `k8sfoams_pending_pods{context="a\"b\\c\nd"} 0`) {
		t.Fatalf("%s", w.Body)
	}
}

// Only /metrics: the dashboard, its API and its auth never answer here.
func TestMetricsHandlerServesNothingElse(t *testing.T) {
	h := newMetricsHandler(&fakeSource{}, foam.DefaultAudit())
	for _, target := range []string{"/", "/healthcheck", "/resources/cpu", "/report.csv"} {
		if w := get(h, target); w.Code != 404 {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// The dashboard and /metrics answer on their own ports, and neither serves
// the other's routes.
func TestRunServesMetricsOnItsOwnListener(t *testing.T) {
	syn, _ := parseSynthetic("2x2")
	dash, metrics := freePort(t), freePort(t)
	o := options{host: "127.0.0.1", port: dash, authMode: "none", synthetic: syn, audit: foam.DefaultAudit(),
		metricsAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(metrics))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, o) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	}()
	// No keep-alive: an idle client connection would hold Shutdown for 5s.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	status := func(port int, path string) int {
		for range 100 {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
			if err == nil {
				resp.Body.Close()
				return resp.StatusCode
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("port %d never answered", port)
		return 0
	}
	if got := status(metrics, "/metrics"); got != 200 {
		t.Errorf("metrics /metrics: %d", got)
	}
	if got := status(metrics, "/healthcheck"); got != 404 {
		t.Errorf("metrics /healthcheck: %d", got)
	}
	if got := status(dash, "/metrics"); got != 404 {
		t.Errorf("dashboard /metrics: %d", got)
	}
}

// A port already taken fails startup instead of leaving the dashboard up
// with metrics silently missing.
func TestRunFailsWhenTheMetricsPortIsTaken(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	syn, _ := parseSynthetic("2x2")
	o := options{host: "127.0.0.1", port: freePort(t), authMode: "none", synthetic: syn, audit: foam.DefaultAudit(),
		metricsAddr: busy.Addr().String()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run(ctx, o); err == nil || !strings.Contains(err.Error(), "metrics") {
		t.Fatalf("got %v", err)
	}
}

// lateSource finishes starting "kind" right after the first Started call,
// as the startup goroutine can mid-scrape.
type lateSource struct {
	*fakeSource
	calls int
}

func (l *lateSource) Started() []string {
	if l.calls++; l.calls == 1 {
		return nil
	}
	return []string{"kind"}
}

// One scrape lists each context once: a duplicate series with a 0 and a 1
// would make Prometheus reject the scrape.
func TestMetricsListEachContextOnce(t *testing.T) {
	w := get(newMetricsHandler(&lateSource{fakeSource: &fakeSource{}}, foam.DefaultAudit()), "/metrics")
	if n := strings.Count(w.Body.String(), "k8sfoams_context_up{"); n != 1 {
		t.Fatalf("%d context_up samples:\n%s", n, w.Body)
	}
}

// A context removed from kubeconfig keeps its cache but is no longer
// reported, so its alert stops instead of firing forever.
func TestMetricsLeaveOutAContextGoneFromKubeconfig(t *testing.T) {
	src := &fakeSource{started: []string{"kind", "staging"}}
	w := get(newMetricsHandler(src, foam.DefaultAudit()), "/metrics")
	if body := w.Body.String(); strings.Contains(body, "staging") || !strings.Contains(body, `k8sfoams_context_up{context="kind"} 1`) {
		t.Fatalf("%s", body)
	}
}

// watchSource is a kubeconfig whose current context can change, with
// started watches like kube.Source; safe for startDefault's goroutine.
type watchSource struct {
	fakeSource
	mu       sync.Mutex
	active   string
	failures int
	started  []string
	asked    []string
}

func (w *watchSource) Contexts() ([]kube.Context, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []kube.Context
	for _, name := range []string{"a", "b"} {
		out = append(out, kube.Context{Context: name, Active: name == w.active})
	}
	return out, nil
}

func (w *watchSource) Started() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.started)
}

func (w *watchSource) Snapshot(_ context.Context, name string) ([]foam.Node, []foam.Pod, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked = append(w.asked, name)
	if w.failures > 0 {
		w.failures--
		return nil, nil, errors.New("connection refused")
	}
	w.started = append(w.started, name)
	return nil, nil, nil
}

func (w *watchSource) set(active string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active = active
}

// eventually polls until the startup loop has caught up.
func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for range 500 {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition never held")
}

// The default context is retried until it starts, and a later switch of
// kubeconfig's current context starts the new one too.
func TestStartDefaultFollowsTheCurrentContext(t *testing.T) {
	src := &watchSource{active: "a", failures: 2}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { startDefault(ctx, src, time.Millisecond); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return slices.Equal(src.Started(), []string{"a"}) })
	src.set("b")
	eventually(t, func() bool { return slices.Equal(src.Started(), []string{"a", "b"}) })
	src.mu.Lock()
	defer src.mu.Unlock()
	if !slices.Equal(src.asked, []string{"a", "a", "a", "b"}) {
		t.Fatalf("asked %q: a started context must not be snapshotted again", src.asked)
	}
}

// With no current context there is nothing to start: no snapshot.
func TestStartDefaultWaitsForACurrentContext(t *testing.T) {
	src := &watchSource{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startDefault(ctx, src, time.Millisecond)
	if len(src.asked) != 0 {
		t.Fatalf("asked %q", src.asked)
	}
}

func TestStartDefaultStopsWithTheServer(t *testing.T) {
	src := &watchSource{active: "a", failures: 1 << 30}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { startDefault(ctx, src, time.Hour); close(done) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("still retrying after shutdown")
	}
}
