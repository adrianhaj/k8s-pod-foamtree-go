package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
)

func ptr[T any](v T) *T { return &v }

func TestMetricsExposition(t *testing.T) {
	src := &fakeSource{
		started: []string{"kind"},
		nodes: []foam.Node{
			{Name: "n1", CPU: 4000, Memory: 8_000_000_000, Pool: "general", Zone: "a", Conditions: map[string]bool{"Ready": true}},
			{Name: "n2", CPU: 2000, Memory: 4_000_000_000, Pool: "general", Zone: "a", Unschedulable: true},
			{Name: "n3", CPU: 8000, Memory: 16_000_000_000, Zone: "b", Conditions: map[string]bool{"Ready": false}},
		},
		pods: []foam.Pod{
			{Name: "web", Namespace: "shop", NodeName: "n1", CPU: 1000, Memory: 2_000_000_000,
				Containers: []foam.Container{{Name: "web", CPU: 1000, Memory: 2_000_000_000, MemoryLimit: ptr[int64](2_000_000_000)}}},
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
	want := `# HELP k8sfoams_capacity_cpu_cores Node CPU capacity per pool and zone.
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

// Nothing started means no samples, not an error: Prometheus still sees the
// families and an up target.
func TestMetricsWithNoStartedContext(t *testing.T) {
	w := get(newMetricsHandler(&fakeSource{}, foam.DefaultAudit()), "/metrics")
	if w.Code != 200 || strings.Contains(w.Body.String(), "{") || !strings.Contains(w.Body.String(), "# TYPE k8sfoams_pending_pods gauge") {
		t.Fatalf("%d\n%s", w.Code, w.Body)
	}
}

// One broken context must not hide the others' numbers.
func TestMetricsSkipsAContextThatFails(t *testing.T) {
	src := &fakeSource{started: []string{"kind"}, err: errors.New("gone")}
	w := get(newMetricsHandler(src, foam.DefaultAudit()), "/metrics")
	if w.Code != 200 || strings.Contains(w.Body.String(), `context="kind"`) {
		t.Fatalf("%d\n%s", w.Code, w.Body)
	}
}

// Context names come from kubeconfig and can hold anything.
func TestMetricsEscapesLabelValues(t *testing.T) {
	src := &fakeSource{started: []string{"a\"b\\c\nd"}}
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
	o := options{host: "127.0.0.1", port: freePort(t), authMode: "none", synthetic: &syntheticSource{nodes: 1, podsPerNode: 1, now: time.Now},
		audit: foam.DefaultAudit(), metricsAddr: busy.Addr().String()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run(ctx, o); err == nil || !strings.Contains(err.Error(), "metrics") {
		t.Fatalf("got %v", err)
	}
}
