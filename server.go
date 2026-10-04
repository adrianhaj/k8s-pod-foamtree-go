package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/auth"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/llm"
)

type source interface {
	Contexts() ([]kube.Context, error)
	Snapshot(ctx context.Context, name string) ([]foam.Node, []foam.Pod, error)
	Logs(ctx context.Context, name string, req kube.LogRequest) (io.ReadCloser, error)
}

const (
	defaultLogTail = 500
	maxLogTail     = 5000
)

// checkLogRequest rejects names the API server would refuse anyway, before
// they become part of a request path.
func checkLogRequest(r kube.LogRequest) error {
	if r.Namespace == "" || r.Pod == "" {
		return errors.New("namespace and pod are required")
	}
	if len(validation.IsDNS1123Label(r.Namespace)) > 0 {
		return fmt.Errorf("invalid namespace %q", r.Namespace)
	}
	if len(validation.IsDNS1123Subdomain(r.Pod)) > 0 {
		return fmt.Errorf("invalid pod name %q", r.Pod)
	}
	if r.Container != "" && len(validation.IsDNS1123Label(r.Container)) > 0 {
		return fmt.Errorf("invalid container name %q", r.Container)
	}
	return nil
}

func logsStatus(err error) int {
	switch {
	case errors.Is(err, kube.ErrUnknownContext), apierrors.IsBadRequest(err):
		return http.StatusBadRequest
	case apierrors.IsNotFound(err):
		return http.StatusNotFound
	case apierrors.IsForbidden(err):
		return http.StatusForbidden
	}
	slog.Warn("logs", "err", err)
	return http.StatusServiceUnavailable
}

// First load of a big cluster can take a while; later requests hit the cache.
const snapshotTimeout = 20 * time.Second

// A generous cap on the free-text /api/fit fields, well above any real
// selector or toleration list, so a client can't force a huge parse.
const maxFitFieldBytes = 4096

// a is nil when auth is off.
func newHandler(src source, static fs.FS, a *auth.Auth, assistant *llm.Proxy) http.Handler {
	app := http.NewServeMux()
	app.HandleFunc("GET /contexts", func(w http.ResponseWriter, r *http.Request) {
		cs, err := src.Contexts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, cs)
	})
	app.HandleFunc("GET /resources/{kind}", func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		axis, ok := map[string]foam.Axis{"cpu": foam.CPU, "memory": foam.Memory}[strings.ToLower(kind)]
		if !ok {
			http.Error(w, "Resource type: "+kind+" is not supported. Supported types are: [cpu, memory]", http.StatusBadRequest)
			return
		}
		if nodes, pods, ok := snapshot(w, r, src); ok {
			writeJSON(w, foam.Treemap(nodes, pods, axis))
		}
	})
	// Plain "attachment": a filename here would override the UI's download name.
	app.HandleFunc("GET /report.json", func(w http.ResponseWriter, r *http.Request) {
		if nodes, pods, ok := snapshot(w, r, src); ok {
			w.Header().Set("Content-Disposition", "attachment")
			writeJSON(w, foam.Report(nodes, pods))
		}
	})
	app.HandleFunc("GET /report.csv", func(w http.ResponseWriter, r *http.Request) {
		if nodes, pods, ok := snapshot(w, r, src); ok {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment")
			writeCSV(w, foam.Report(nodes, pods))
		}
	})
	// Read-only dry run, so a GET: nothing to forge, and the link can be shared.
	app.HandleFunc("GET /api/fit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q.Get("nodeSelector")) > maxFitFieldBytes || len(q.Get("tolerations")) > maxFitFieldBytes {
			http.Error(w, "nodeSelector and tolerations must be at most 4096 bytes", http.StatusBadRequest)
			return
		}
		p, err := foam.Hypothetical(q.Get("cpu"), q.Get("memory"), q.Get("nodeSelector"), q.Get("tolerations"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if nodes, pods, ok := snapshot(w, r, src); ok {
			writeJSON(w, foam.Fit(nodes, pods, p))
		}
	})
	app.HandleFunc("GET /api/drain", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("node")
		if len(name) > maxFitFieldBytes {
			http.Error(w, "node must be at most 4096 bytes", http.StatusBadRequest)
			return
		}
		nodes, pods, ok := snapshot(w, r, src)
		if !ok {
			return
		}
		if d, ok := foam.Drain(nodes, pods, name); ok {
			writeJSON(w, d)
			return
		}
		http.Error(w, "no node "+strconv.Quote(name), http.StatusNotFound)
	})
	app.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		req := kube.LogRequest{Namespace: q.Get("namespace"), Pod: q.Get("pod"), Container: q.Get("container"),
			Tail: defaultLogTail, Previous: q.Get("previous") == "1"}
		if t := q.Get("tail"); t != "" {
			n, err := strconv.ParseInt(t, 10, 64)
			if err != nil || n < 1 || n > maxLogTail {
				http.Error(w, fmt.Sprintf("tail must be 1 to %d", maxLogTail), http.StatusBadRequest)
				return
			}
			req.Tail = n
		}
		if err := checkLogRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
		defer cancel()
		rc, err := src.Logs(ctx, q.Get("context"), req)
		if err != nil {
			http.Error(w, err.Error(), logsStatus(err))
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := io.Copy(w, rc); err != nil {
			slog.Warn("logs", "err", err)
		}
	})
	if assistant != nil {
		app.HandleFunc("GET /api/llm/config", assistant.ServeConfig)
		app.HandleFunc("POST /api/llm/chat", assistant.ServeChat)
	}
	app.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := auth.UserFrom(r.Context()); ok {
			writeJSON(w, map[string]string{"auth": "oidc", "email": u.Email, "name": u.Name})
			return
		}
		writeJSON(w, map[string]string{"auth": "none"})
	})
	app.Handle("GET /", http.FileServerFS(static))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthcheck", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	if a == nil {
		root.Handle("/", app)
	} else {
		a.Register(root)
		root.Handle("/", a.Require(app))
	}
	return secure(root)
}

// snapshot reads the ?context= cluster, writing the error response itself
// when that fails.
func snapshot(w http.ResponseWriter, r *http.Request, src source) ([]foam.Node, []foam.Pod, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), snapshotTimeout)
	defer cancel()
	nodes, pods, err := src.Snapshot(ctx, r.URL.Query().Get("context"))
	switch {
	case errors.Is(err, kube.ErrUnknownContext):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		slog.Warn("snapshot", "err", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		return nodes, pods, true
	}
	return nil, nil, false
}

func writeCSV(w http.ResponseWriter, rows []foam.ReportRow) {
	opt := func(v *int64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatInt(*v, 10)
	}
	cw := csv.NewWriter(w)
	cw.Write([]string{"node", "zone", "pool", "instance_type", "node_cpu_m", "node_memory_bytes", "node_warnings",
		"namespace", "pod", "qos", "cpu_request_m", "cpu_limit_m", "memory_request_bytes", "memory_limit_bytes", "findings"})
	for _, r := range rows {
		cw.Write([]string{r.Node, r.Zone, r.Pool, r.InstanceType, strconv.FormatInt(r.NodeCPU, 10),
			strconv.FormatInt(r.NodeMemoryBytes, 10), strings.Join(r.NodeWarnings, " "),
			r.Namespace, r.Pod, r.QOS, strconv.FormatInt(r.CPU, 10), opt(r.CPULimit),
			strconv.FormatInt(r.MemoryBytes, 10), opt(r.MemoryLimitBytes), strings.Join(r.Findings, " ")})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		slog.Warn("write response", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}

// Fonts are the only third-party load; everything else is embedded.
const csp = "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; img-src 'self' data:; frame-ancestors 'none'"

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
