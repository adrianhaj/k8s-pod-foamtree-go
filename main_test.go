package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
)

type fakeSource struct {
	nodes  []foam.Node
	pods   []foam.Pod
	err    error
	asked  string
	logs   string
	logErr error
	logReq kube.LogRequest
}

func (f *fakeSource) Logs(_ context.Context, name string, req kube.LogRequest) (io.ReadCloser, error) {
	f.asked, f.logReq = name, req
	if f.logErr != nil {
		return nil, f.logErr
	}
	return io.NopCloser(strings.NewReader(f.logs)), nil
}

func (f *fakeSource) Contexts() ([]kube.Context, error) {
	return []kube.Context{{Context: "kind", Active: true}}, nil
}

func (f *fakeSource) Snapshot(_ context.Context, name string) ([]foam.Node, []foam.Pod, error) {
	f.asked = name
	return f.nodes, f.pods, f.err
}

var static = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>k8sfoams</title>")}}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}

func TestRoutes(t *testing.T) {
	src := &fakeSource{
		nodes: []foam.Node{{Name: "minikube", CPU: 2000, Memory: 1_000_000_000}},
		pods:  []foam.Pod{{Name: "etcd", NodeName: "minikube", CPU: 150, Containers: []foam.Container{{Name: "etcd", CPU: 150}}}},
	}
	h := newHandler(src, static, nil)
	cases := []struct {
		target string
		code   int
		body   string
	}{
		{"/healthcheck", 200, `{"status":"ok"}`},
		{"/contexts", 200, `[{"context":"kind","active":true}]`},
		{"/resources/cpu", 200, `"label":"minikube","weight":2000`},
		{"/resources/MEMORY", 200, `"label":"minikube","weight":1000000`},
		{"/resources/disk", 400, "Resource type: disk is not supported. Supported types are: [cpu, memory]"},
		{"/api/me", 200, `{"auth":"none"}`},
		{"/", 200, "<title>k8sfoams</title>"},
	}
	for _, tc := range cases {
		w := get(h, tc.target)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
			t.Errorf("%s: %d %q", tc.target, w.Code, w.Body)
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", tc.target)
		}
	}
}

func TestResourcesPassesContextAndMapsErrors(t *testing.T) {
	src := &fakeSource{}
	h := newHandler(src, static, nil)
	if get(h, "/resources/cpu?context=kind-a"); src.asked != "kind-a" {
		t.Fatalf("context not forwarded: %q", src.asked)
	}
	src.err = fmt.Errorf("%w %q", kube.ErrUnknownContext, "prod")
	if w := get(h, "/resources/cpu?context=prod"); w.Code != 400 {
		t.Fatalf("unknown context: %d", w.Code)
	}
	src.err = errors.New(`cluster "kind": Unauthorized`)
	if w := get(h, "/resources/cpu"); w.Code != 503 || !strings.Contains(w.Body.String(), "Unauthorized") {
		t.Fatalf("cluster error: %d %s", w.Code, w.Body)
	}
}

func TestReportRoutes(t *testing.T) {
	src := &fakeSource{
		nodes: []foam.Node{{Name: "minikube", CPU: 2000, Memory: 1_000_000_000, Zone: "z1"}},
		pods: []foam.Pod{
			{Name: "etcd", Namespace: "kube-system", NodeName: "minikube", CPU: 150, Memory: 100_000_000},
			{Name: "pending", Namespace: "dev", CPU: 100},
		},
	}
	h := newHandler(src, static, nil)
	w := get(h, "/report.csv?context=kind")
	want := "node,zone,pool,instance_type,node_cpu_m,node_memory_bytes,node_warnings,namespace,pod,qos," +
		"cpu_request_m,cpu_limit_m,memory_request_bytes,memory_limit_bytes,findings\n" +
		"minikube,z1,,,2000,1000000000,,kube-system,etcd,,150,,100000000,,\n" +
		",,,,0,0,,dev,pending,,100,,0,,\n"
	if w.Code != 200 || w.Body.String() != want || src.asked != "kind" ||
		w.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		w.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("csv: %d %v\n%s", w.Code, w.Header(), w.Body)
	}
	w = get(h, "/report.json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pod":"etcd","qos":"","cpu":150,"cpuLimit":null,"memoryBytes":100000000`) {
		t.Fatalf("json: %d %s", w.Code, w.Body)
	}
	src.err = fmt.Errorf("%w %q", kube.ErrUnknownContext, "prod")
	if w := get(h, "/report.csv?context=prod"); w.Code != 400 {
		t.Fatalf("unknown context: %d", w.Code)
	}
}

func TestFlags(t *testing.T) {
	cases := []struct {
		args []string
		ok   bool
	}{
		{nil, true},
		{[]string{"--host", "0.0.0.0"}, false},
		{[]string{"--host", "0.0.0.0", "--allow-unauthenticated"}, true},
		{[]string{"--host", "0.0.0.0", "--auth", "oidc"}, true},
		{[]string{"--host", "::1"}, true},
		{[]string{"--port", "x"}, false},
		{[]string{"--auth", "basic"}, false},
	}
	for _, tc := range cases {
		if _, err := parseFlags(tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: err=%v", tc.args, err)
		}
	}
	// --version skips validation: it must print even with flags that would be refused.
	for _, args := range [][]string{{"--version"}, {"-v", "--host", "0.0.0.0"}} {
		if o, err := parseFlags(args); err != nil || !o.version {
			t.Errorf("%v: version=%v err=%v", args, o.version, err)
		}
	}
	o, _ := parseFlags([]string{"--auth", "oidc", "--oidc-allowed-emails", "a@x.com, *@y.com,"})
	if fmt.Sprint(o.oidc.AllowedEmails, o.oidc.Scopes) != "[a@x.com *@y.com] [openid email profile]" {
		t.Fatalf("lists: %v %v", o.oidc.AllowedEmails, o.oidc.Scopes)
	}
}

func TestSessionKey(t *testing.T) {
	if k, err := sessionKey(""); err != nil || len(k) != 32 {
		t.Fatalf("random key: %d %v", len(k), err)
	}
	if _, err := sessionKey("c2hvcnQ="); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestFitRoute(t *testing.T) {
	src := &fakeSource{nodes: []foam.Node{
		{Name: "big", AllocCPU: 8000, AllocMemory: 32_000_000_000, AllocPods: 110},
		{Name: "small", AllocCPU: 1000, AllocMemory: 32_000_000_000, AllocPods: 110},
	}}
	h := newHandler(src, static, nil)
	w := get(h, "/api/fit?context=kind-a&cpu=2&memory=1Gi")
	want := `[{"node":"big","reasons":[]},{"node":"small","reasons":["insufficient cpu: requires 2000m, available 1000m"]}]` + "\n"
	if w.Code != 200 || w.Body.String() != want || src.asked != "kind-a" {
		t.Fatalf("%d %s (context %q)", w.Code, w.Body, src.asked)
	}
	if w := get(h, "/api/fit?cpu=lots"); w.Code != 400 || !strings.Contains(w.Body.String(), `cpu "lots"`) {
		t.Fatalf("bad input: %d %s", w.Code, w.Body)
	}
	src.err = errors.New("Unauthorized")
	if w := get(h, "/api/fit?cpu=1"); w.Code != 503 {
		t.Fatalf("cluster error: %d", w.Code)
	}
}

func TestFitRouteRejectsOversizedFreeText(t *testing.T) {
	h := newHandler(&fakeSource{}, static, nil)
	big := strings.Repeat("a", 4097)
	if w := get(h, "/api/fit?tolerations="+big); w.Code != 400 {
		t.Fatalf("oversized tolerations: %d", w.Code)
	}
	if w := get(h, "/api/fit?nodeSelector="+big); w.Code != 400 {
		t.Fatalf("oversized nodeSelector: %d", w.Code)
	}
}

func TestDrainRoute(t *testing.T) {
	src := &fakeSource{
		nodes: []foam.Node{
			{Name: "a", AllocCPU: 4000, AllocMemory: 8_000_000_000, AllocPods: 110},
			{Name: "b", AllocCPU: 4000, AllocMemory: 8_000_000_000, AllocPods: 110},
		},
		pods: []foam.Pod{{Name: "web", Namespace: "ns", NodeName: "a", Controller: "ReplicaSet", CPU: 100}},
	}
	h := newHandler(src, static, nil)
	w := get(h, "/api/drain?context=kind-a&node=a")
	want := `{"moved":[{"pod":"ns/web","node":"b"}],"pending":[],"ignored":[],"unmanaged":[]}` + "\n"
	if w.Code != 200 || w.Body.String() != want || src.asked != "kind-a" {
		t.Fatalf("%d %s (context %q)", w.Code, w.Body, src.asked)
	}
	if w := get(h, "/api/drain?node=gone"); w.Code != 404 {
		t.Fatalf("unknown node: %d %s", w.Code, w.Body)
	}
}

func TestDrainRouteRejectsOversizedNode(t *testing.T) {
	h := newHandler(&fakeSource{}, static, nil)
	big := strings.Repeat("a", 4097)
	if w := get(h, "/api/drain?node="+big); w.Code != 400 {
		t.Fatalf("oversized node: %d", w.Code)
	}
}

func TestLogsRoute(t *testing.T) {
	src := &fakeSource{logs: "line 1\nline 2\n"}
	h := newHandler(src, static, nil)
	w := get(h, "/api/logs?context=kind&namespace=payments&pod=api-7f9c4-x2kqp&container=api&tail=200&previous=1")
	if w.Code != 200 || w.Body.String() != "line 1\nline 2\n" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("%d %q %q", w.Code, w.Body, w.Header())
	}
	want := kube.LogRequest{Namespace: "payments", Pod: "api-7f9c4-x2kqp", Container: "api", Tail: 200, Previous: true}
	if src.asked != "kind" || src.logReq != want {
		t.Fatalf("asked %q with %+v", src.asked, src.logReq)
	}
	get(h, "/api/logs?namespace=ns&pod=p")
	if src.logReq.Tail != 500 || src.logReq.Previous {
		t.Fatalf("defaults: %+v", src.logReq)
	}
}

func TestLogsRouteRejectsBadInput(t *testing.T) {
	h := newHandler(&fakeSource{}, static, nil)
	for _, target := range []string{
		"/api/logs?pod=p",
		"/api/logs?namespace=ns",
		"/api/logs?namespace=ns&pod=p&tail=0",
		"/api/logs?namespace=ns&pod=p&tail=5001",
		"/api/logs?namespace=ns&pod=p&tail=x",
		"/api/logs?namespace=..&pod=p",
		"/api/logs?namespace=ns&pod=a%2Fb",
		"/api/logs?namespace=ns&pod=p&container=Bad_Name",
	} {
		if w := get(h, target); w.Code != 400 {
			t.Errorf("%s: %d %q", target, w.Code, w.Body)
		}
	}
}

func TestLogsRouteMapsErrors(t *testing.T) {
	gone := apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "p")
	noPrev := apierrors.NewBadRequest(`previous terminated container "app" in pod "p" not found`)
	for _, tc := range []struct {
		err  error
		code int
		body string
	}{
		{gone, 404, `pods "p" not found`},
		{noPrev, 400, "previous terminated container"},
		{fmt.Errorf("%w %q", kube.ErrUnknownContext, "prod"), 400, "unknown context"},
		{apierrors.NewForbidden(schema.GroupResource{Resource: "pods/log"}, "p", errors.New("rbac")), 403, "forbidden"},
		{errors.New("dial tcp: i/o timeout"), 503, "i/o timeout"},
	} {
		w := get(newHandler(&fakeSource{logErr: tc.err}, static, nil), "/api/logs?namespace=ns&pod=p")
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
			t.Errorf("%v: %d %q", tc.err, w.Code, w.Body)
		}
	}
}
