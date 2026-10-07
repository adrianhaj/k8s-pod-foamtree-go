package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
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
		var clusters []cluster
		if name, ok := defaultNotStarted(src); ok {
			clusters = append(clusters, cluster{name: name})
		}
		for _, name := range src.Started() {
			ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
			nodes, pods, err := src.Snapshot(ctx, name)
			cancel()
			if err != nil {
				slog.Warn("metrics", "context", name, "err", err)
				clusters = append(clusters, cluster{name: name})
				continue
			}
			s := foam.Summarize(nodes, pods, audit)
			clusters = append(clusters, cluster{name: name, summary: &s})
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

// defaultNotStarted names the default context while its watch has not
// started, so the scrape reports it down rather than leaving it out.
func defaultNotStarted(src source) (string, bool) {
	cs, err := src.Contexts()
	if err != nil {
		slog.Warn("metrics", "err", err)
		return "", false
	}
	for _, c := range cs {
		if c.Active && !slices.Contains(src.Started(), c.Context) {
			return c.Context, true
		}
	}
	return "", false
}

// cluster is one context's scrape; summary is nil when it is down.
type cluster struct {
	name    string
	summary *foam.Summary
}

type emitFunc func(labels string, v float64)

type family struct {
	name, help string
	// write runs only for clusters that are up, unless down is set.
	down  bool
	write func(emit emitFunc, c cluster)
}

func contextLabel(c cluster) string { return `context=` + quote(c.name) }

// eachGroup visits a cluster's pool/zone groups, sorted by pool, then zone.
func eachGroup(emit emitFunc, c cluster, v func(g foam.GroupSummary) float64) {
	for _, g := range c.summary.Groups {
		emit(contextLabel(c)+`,pool=`+quote(g.Pool)+`,zone=`+quote(g.Zone), v(g))
	}
}

func ratio(used, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total)
}

var families = []family{
	{"k8sfoams_context_up", "1 when the context's watch cache answered this scrape, 0 when it failed or the default context has not started.", true, func(emit emitFunc, c cluster) {
		up := 0.0
		if c.summary != nil {
			up = 1
		}
		emit(contextLabel(c), up)
	}},
	{"k8sfoams_capacity_cpu_cores", "Node CPU capacity per pool and zone.", false, func(emit emitFunc, c cluster) {
		eachGroup(emit, c, func(g foam.GroupSummary) float64 { return float64(g.CPU) / 1000 })
	}},
	{"k8sfoams_headroom_cpu_cores", "Node CPU capacity no pod requests, per pool and zone: the empty foam.", false, func(emit emitFunc, c cluster) {
		eachGroup(emit, c, func(g foam.GroupSummary) float64 { return float64(g.CPU-g.CPURequested) / 1000 })
	}},
	{"k8sfoams_capacity_memory_bytes", "Node memory capacity per pool and zone.", false, func(emit emitFunc, c cluster) {
		eachGroup(emit, c, func(g foam.GroupSummary) float64 { return float64(g.Memory) })
	}},
	{"k8sfoams_headroom_memory_bytes", "Node memory capacity no pod requests, per pool and zone: the empty foam.", false, func(emit emitFunc, c cluster) {
		eachGroup(emit, c, func(g foam.GroupSummary) float64 { return float64(g.Memory - g.MemoryRequested) })
	}},
	{"k8sfoams_requested_ratio", "Requested share of node capacity per pool and zone, 0.8 = 80%.", false, func(emit emitFunc, c cluster) {
		for _, g := range c.summary.Groups {
			l := contextLabel(c) + `,pool=` + quote(g.Pool) + `,zone=` + quote(g.Zone)
			emit(l+`,resource="cpu"`, ratio(g.CPURequested, g.CPU))
			emit(l+`,resource="memory"`, ratio(g.MemoryRequested, g.Memory))
		}
	}},
	{"k8sfoams_audit_findings", "Pods with each audit finding; rules turned off with --audit-disable are left out.", false, func(emit emitFunc, c cluster) {
		for _, r := range foam.AuditRules {
			if n, ok := c.summary.Findings[r]; ok {
				emit(contextLabel(c)+`,rule=`+quote(r), float64(n))
			}
		}
	}},
	{"k8sfoams_node_warnings", "Nodes with each warning.", false, func(emit emitFunc, c cluster) {
		for _, w := range foam.NodeWarnings {
			emit(contextLabel(c)+`,warning=`+quote(w), float64(c.summary.Warnings[w]))
		}
	}},
	{"k8sfoams_pending_pods", "Pods no node has taken yet.", false, func(emit emitFunc, c cluster) {
		emit(contextLabel(c), float64(c.summary.Pending))
	}},
}

// writeMetrics writes every family with its HELP and TYPE, even when no
// context is up. A cluster that is down appears only in context_up.
func writeMetrics(w *bufio.Writer, clusters []cluster) {
	for _, f := range families {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", f.name, f.help, f.name)
		emit := func(labels string, v float64) {
			fmt.Fprintf(w, "%s{%s} %s\n", f.name, labels, strconv.FormatFloat(v, 'f', -1, 64))
		}
		for _, c := range clusters {
			if f.down || c.summary != nil {
				f.write(emit, c)
			}
		}
	}
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// quote escapes a label value as the text format wants: backslash, double
// quote and newline only, unlike Go's %q.
func quote(s string) string { return `"` + labelEscaper.Replace(s) + `"` }
