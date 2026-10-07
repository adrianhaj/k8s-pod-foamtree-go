package main

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
)

// newMetricsHandler serves GET /metrics in the Prometheus text format, on its
// own listener: no dashboard, no API and no sign-in there. Every scrape reads
// the watch cache of each context already started, never starting one.
func newMetricsHandler(src source, audit foam.Audit) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		var clusters []clusterMetrics
		for _, name := range src.Started() {
			ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
			nodes, pods, err := src.Snapshot(ctx, name)
			cancel()
			if err != nil {
				slog.Warn("metrics", "context", name, "err", err)
				continue
			}
			clusters = append(clusters, measure(name, nodes, pods, audit))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		bw := bufio.NewWriter(w)
		writeMetrics(bw, clusters)
		if err := bw.Flush(); err != nil {
			slog.Warn("write response", "err", err)
		}
	})
	return mux
}

type poolZone struct{ pool, zone string }

// Sums stay in the scheduler's units, millicores and bytes, until written.
type capacity struct{ cpu, cpuUsed, mem, memUsed int64 }

type clusterMetrics struct {
	context string
	groups  map[poolZone]*capacity
	// The rules --audit-disable left on, in AuditRules order.
	rules    []string
	findings map[string]int
	warnings map[string]int
	pending  int
}

// measure counts like the dashboard: node capacity, the requests of pods on
// a known node, and findings for every pod, a pod on no known node judged
// against the zero Node as Report does.
func measure(name string, nodes []foam.Node, pods []foam.Pod, audit foam.Audit) clusterMetrics {
	m := clusterMetrics{context: name, groups: map[poolZone]*capacity{}, findings: map[string]int{}, warnings: map[string]int{},
		rules: slices.DeleteFunc(slices.Clone(foam.AuditRules), func(r string) bool { return audit.Disabled[r] })}
	byName := map[string]foam.Node{}
	groupOf := map[string]*capacity{}
	for _, n := range nodes {
		byName[n.Name] = n
		k := poolZone{n.Pool, n.Zone}
		if m.groups[k] == nil {
			m.groups[k] = &capacity{}
		}
		g := m.groups[k]
		g.cpu += n.CPU
		g.mem += n.Memory
		groupOf[n.Name] = g
		for _, w := range foam.Warnings(n) {
			m.warnings[w]++
		}
	}
	for _, p := range pods {
		if p.NodeName == "" {
			m.pending++
		}
		if g := groupOf[p.NodeName]; g != nil {
			g.cpuUsed += p.CPU
			g.memUsed += p.Memory
		}
		for _, f := range foam.Findings(p, byName[p.NodeName], audit) {
			m.findings[f]++
		}
	}
	return m
}

type family struct {
	name, help string
	write      func(emit func(labels string, v float64), c clusterMetrics)
}

// eachGroup visits a cluster's pool/zone groups sorted by pool, then zone.
func eachGroup(c clusterMetrics, f func(labels string, g *capacity)) {
	keys := slices.SortedFunc(maps.Keys(c.groups), func(a, b poolZone) int {
		return cmp.Or(cmp.Compare(a.pool, b.pool), cmp.Compare(a.zone, b.zone))
	})
	for _, k := range keys {
		f(fmt.Sprintf(`context=%s,pool=%s,zone=%s`, quote(c.context), quote(k.pool), quote(k.zone)), c.groups[k])
	}
}

func ratio(used, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total)
}

var families = []family{
	{"k8sfoams_capacity_cpu_cores", "Node CPU capacity per pool and zone.", func(emit func(string, float64), c clusterMetrics) {
		eachGroup(c, func(l string, g *capacity) { emit(l, float64(g.cpu)/1000) })
	}},
	{"k8sfoams_headroom_cpu_cores", "Node CPU capacity no pod requests, per pool and zone: the empty foam.", func(emit func(string, float64), c clusterMetrics) {
		eachGroup(c, func(l string, g *capacity) { emit(l, float64(g.cpu-g.cpuUsed)/1000) })
	}},
	{"k8sfoams_capacity_memory_bytes", "Node memory capacity per pool and zone.", func(emit func(string, float64), c clusterMetrics) {
		eachGroup(c, func(l string, g *capacity) { emit(l, float64(g.mem)) })
	}},
	{"k8sfoams_headroom_memory_bytes", "Node memory capacity no pod requests, per pool and zone: the empty foam.", func(emit func(string, float64), c clusterMetrics) {
		eachGroup(c, func(l string, g *capacity) { emit(l, float64(g.mem-g.memUsed)) })
	}},
	{"k8sfoams_requested_ratio", "Requested share of node capacity per pool and zone, 0.8 = 80%.", func(emit func(string, float64), c clusterMetrics) {
		eachGroup(c, func(l string, g *capacity) {
			emit(l+`,resource="cpu"`, ratio(g.cpuUsed, g.cpu))
			emit(l+`,resource="memory"`, ratio(g.memUsed, g.mem))
		})
	}},
	{"k8sfoams_audit_findings", "Pods with each audit finding; rules turned off with --audit-disable are left out.", func(emit func(string, float64), c clusterMetrics) {
		for _, r := range c.rules {
			emit(`context=`+quote(c.context)+`,rule=`+quote(r), float64(c.findings[r]))
		}
	}},
	{"k8sfoams_node_warnings", "Nodes with each warning.", func(emit func(string, float64), c clusterMetrics) {
		for _, w := range foam.NodeWarnings {
			emit(`context=`+quote(c.context)+`,warning=`+quote(w), float64(c.warnings[w]))
		}
	}},
	{"k8sfoams_pending_pods", "Pods no node has taken yet.", func(emit func(string, float64), c clusterMetrics) {
		emit(`context=`+quote(c.context), float64(c.pending))
	}},
}

// writeMetrics writes every family, with its HELP and TYPE even when no
// context is started, and a zero for every rule or warning a cluster lacks,
// so an alert sees 0 rather than no data.
func writeMetrics(w *bufio.Writer, clusters []clusterMetrics) {
	for _, f := range families {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", f.name, f.help, f.name)
		emit := func(labels string, v float64) {
			fmt.Fprintf(w, "%s{%s} %s\n", f.name, labels, strconv.FormatFloat(v, 'f', -1, 64))
		}
		for _, c := range clusters {
			f.write(emit, c)
		}
	}
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// quote escapes a label value as the text format wants: backslash, double
// quote and newline only, unlike Go's %q.
func quote(s string) string { return `"` + labelEscaper.Replace(s) + `"` }
