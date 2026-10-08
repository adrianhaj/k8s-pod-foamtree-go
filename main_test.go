package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/llm"
)

type fakeSource struct {
	nodes  []foam.Node
	pods   []foam.Pod
	err    error
	asked  string
	logs   string
	logErr error
	logReq kube.LogRequest
	// Contexts whose watches /metrics may read.
	started []string
	// nil means one active context, "kind".
	contexts []kube.Context
}

func (f *fakeSource) Started() []string { return f.started }

func (f *fakeSource) Logs(_ context.Context, name string, req kube.LogRequest) (io.ReadCloser, error) {
	f.asked, f.logReq = name, req
	if f.logErr != nil {
		return nil, f.logErr
	}
	return io.NopCloser(strings.NewReader(f.logs)), nil
}

func (f *fakeSource) Contexts() ([]kube.Context, error) {
	if f.contexts != nil {
		return f.contexts, nil
	}
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
	h := newHandler(src, static, nil, nil, foam.DefaultAudit())
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
	h := newHandler(src, static, nil, nil, foam.DefaultAudit())
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
	h := newHandler(pricedSource{src, foam.Prices{{Hourly: 0.1}}}, static, nil, nil, foam.DefaultAudit())
	w := get(h, "/report.csv?context=kind")
	want := "node,zone,pool,instance_type,node_cpu_m,node_memory_bytes,node_warnings,namespace,pod,qos," +
		"cpu_request_m,cpu_limit_m,memory_request_bytes,memory_limit_bytes,findings,node_hourly_usd,pod_hourly_usd\n" +
		"minikube,z1,,,2000,1000000000,,kube-system,etcd,,150,,100000000,,,0.100000,0.010000\n" +
		",,,,0,0,,dev,pending,,100,,0,,,,\n"
	if w.Code != 200 || w.Body.String() != want || src.asked != "kind" ||
		w.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		w.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("csv: %d %v\n%s", w.Code, w.Header(), w.Body)
	}
	w = get(h, "/report.json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"pod":"etcd","qos":"","cpu":150,"cpuLimit":null,"memoryBytes":100000000`) {
		t.Fatalf("json: %d %s", w.Code, w.Body)
	}
	w = get(h, "/report.csv?groupBy=namespace")
	if want := "group,pods,unpriced_pods,cpu_request_m,memory_request_bytes,hourly_usd\nkube-system,1,0,150,100000000,0.010000\n"; w.Code != 200 || w.Body.String() != want {
		t.Fatalf("showback csv: %d\n%s", w.Code, w.Body)
	}
	if w := get(h, "/report.json?groupBy=label:team"); w.Code != 200 || !strings.Contains(w.Body.String(), `"group":"","pods":1`) {
		t.Fatalf("showback json: %d %s", w.Code, w.Body)
	}
	if w := get(h, "/report.json?groupBy=pool"); w.Code != 400 {
		t.Fatalf("bad groupBy: %d", w.Code)
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
		{[]string{"--audit-monolith", "100"}, true},
		{[]string{"--audit-monolith", "0"}, false},
		{[]string{"--audit-monolith", "-5"}, false},
		{[]string{"--audit-monolith", "100.5"}, false},
		{[]string{"--audit-monolith", "NaN"}, false},
		{[]string{"--audit-monolith", "x"}, false},
		{[]string{"--audit-ratio", "1.5"}, true},
		{[]string{"--audit-ratio", "1"}, false},
		{[]string{"--audit-ratio", "Inf"}, false},
		{[]string{"--audit-ratio", "NaN"}, false},
		{[]string{"--audit-ratio-min-share", "0"}, true},
		{[]string{"--audit-ratio-min-share", "-1"}, false},
		{[]string{"--audit-ratio-min-share", "101"}, false},
		{[]string{"--audit-disable", ""}, true},
		{[]string{"--audit-disable", "monolith, missing-limits,"}, true},
		{[]string{"--audit-disable", "sidecars"}, false},
	}
	for _, tc := range cases {
		if _, err := parseFlags(tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: err=%v", tc.args, err)
		}
	}
	// --version skips validation: it must print even with flags that would be refused.
	for _, args := range [][]string{{"--version"}, {"-v", "--host", "0.0.0.0"}, {"-v", "--audit-monolith", "0"}} {
		if o, err := parseFlags(args); err != nil || !o.version {
			t.Errorf("%v: version=%v err=%v", args, o.version, err)
		}
	}
	o, _ := parseFlags([]string{"--auth", "oidc", "--oidc-allowed-emails", "a@x.com, *@y.com,"})
	if fmt.Sprint(o.oidc.AllowedEmails, o.oidc.Scopes) != "[a@x.com *@y.com] [openid email profile]" {
		t.Fatalf("lists: %v %v", o.oidc.AllowedEmails, o.oidc.Scopes)
	}
	if o, err := parseFlags(nil); err != nil || !reflect.DeepEqual(o.audit, foam.DefaultAudit()) {
		t.Fatalf("default audit: %+v err=%v", o.audit, err)
	}
	o, err := parseFlags([]string{"--audit-monolith", "70", "--audit-ratio", "2", "--audit-ratio-min-share", "5", "--audit-disable", "monolith,"})
	want := foam.Audit{MonolithShare: 0.7, RatioFactor: 2, RatioMinShare: 0.05, Disabled: map[string]bool{"monolith": true}}
	if err != nil || !reflect.DeepEqual(o.audit, want) {
		t.Fatalf("custom audit: %+v err=%v", o.audit, err)
	}
	if _, err := parseFlags([]string{"--audit-disable", "sidecars"}); err == nil || !strings.Contains(err.Error(), "ratio-asymmetry") {
		t.Fatalf("unknown rule should list the valid ones: %v", err)
	}
}

func TestPricesFlag(t *testing.T) {
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.csv"), filepath.Join(dir, "bad.csv")
	os.WriteFile(good, []byte("instance_type,region,capacity_type,hourly_usd\nm5.xlarge,,,0.192\n"), 0o600)
	os.WriteFile(bad, []byte("instance_type,region,capacity_type,hourly_usd\nm5.xlarge,,,free\n"), 0o600)
	if o, err := parseFlags([]string{"--prices", good}); err != nil || len(o.prices) != 1 {
		t.Fatalf("good: %v %v", o.prices, err)
	}
	for _, f := range []string{bad, filepath.Join(dir, "missing.csv")} {
		if _, err := parseFlags([]string{"--prices", f}); err == nil || !strings.HasPrefix(err.Error(), "--prices: ") {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestPricedResources(t *testing.T) {
	src := &fakeSource{
		nodes: []foam.Node{{Name: "a", CPU: 2000, Memory: 1_000_000_000, InstanceType: "m5"}, {Name: "b", CPU: 2000, Memory: 1_000_000_000}},
		pods: []foam.Pod{{Name: "etcd", NodeName: "a", CPU: 1000, Containers: []foam.Container{{Name: "etcd", CPU: 500}}},
			{Name: "web", NodeName: "b", CPU: 1000}},
	}
	h := newHandler(pricedSource{src, foam.Prices{{InstanceType: "m5", Hourly: 0.2}}}, static, nil, nil, foam.DefaultAudit())
	var tree struct {
		Groups []struct {
			Label      string
			HourlyCost *float64
			Groups     []map[string]any
		}
	}
	if err := json.Unmarshal(get(h, "/resources/cpu").Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	a, b := tree.Groups[0], tree.Groups[1]
	if a.HourlyCost == nil || *a.HourlyCost != 0.2 || a.Groups[0]["hourlyCost"] != 0.1 ||
		a.Groups[0]["groups"].([]any)[0].(map[string]any)["hourlyCost"] != 0.05 {
		t.Errorf("priced node: %v %v", a.HourlyCost, a.Groups[0])
	}
	if _, ok := b.Groups[0]["hourlyCost"]; b.HourlyCost != nil || ok {
		t.Errorf("unpriced node: %v %v", b.HourlyCost, b.Groups[0])
	}
}

func TestMetricsFlags(t *testing.T) {
	cases := []struct {
		args []string
		ok   bool
	}{
		{nil, true},
		{[]string{"--metrics-addr", ":9090"}, true},
		{[]string{"--metrics-addr", "0.0.0.0:9090"}, true},
		{[]string{"--metrics-addr", "[::1]:9090"}, true},
		{[]string{"--metrics-addr", "9090"}, false},
		{[]string{"--metrics-addr", "localhost:"}, false},
		{[]string{"--metrics-addr", ":http"}, false},
		{[]string{"--metrics-addr", ":0"}, false},
		{[]string{"--metrics-addr", ":65536"}, false},
		{[]string{"--metrics-addr", ":8080"}, false},
		{[]string{"--metrics-addr", "10.0.0.1:8080"}, false},
		{[]string{"--metrics-addr", ":8080", "--port", "9000"}, true},
		{[]string{"--metrics-addr", ":9000", "--port", "9000"}, false},
	}
	for _, tc := range cases {
		if _, err := parseFlags(tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: err=%v", tc.args, err)
		}
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
	h := newHandler(src, static, nil, nil, foam.DefaultAudit())
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
	h := newHandler(&fakeSource{}, static, nil, nil, foam.DefaultAudit())
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
	h := newHandler(src, static, nil, nil, foam.DefaultAudit())
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
	h := newHandler(&fakeSource{}, static, nil, nil, foam.DefaultAudit())
	big := strings.Repeat("a", 4097)
	if w := get(h, "/api/drain?node="+big); w.Code != 400 {
		t.Fatalf("oversized node: %d", w.Code)
	}
}

func TestLogsRoute(t *testing.T) {
	src := &fakeSource{logs: "line 1\nline 2\n"}
	h := newHandler(src, static, nil, nil, foam.DefaultAudit())
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
	h := newHandler(&fakeSource{}, static, nil, nil, foam.DefaultAudit())
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
		w := get(newHandler(&fakeSource{logErr: tc.err}, static, nil, nil, foam.DefaultAudit()), "/api/logs?namespace=ns&pod=p")
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
			t.Errorf("%v: %d %q", tc.err, w.Code, w.Body)
		}
	}
}

func TestLoopbackHostOnly(t *testing.T) {
	h := loopbackHostOnly(newHandler(&fakeSource{}, static, nil, nil, foam.DefaultAudit()))
	for host, want := range map[string]int{
		"localhost:8080": 200, "127.0.0.1:8080": 200, "[::1]:8080": 200, "localhost": 200,
		"evil.example:8080": 421, "127.0.0.1.nip.io:8080": 421,
	} {
		r := httptest.NewRequest("GET", "/api/me", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: got %d, want %d", host, w.Code, want)
		}
	}
}

func TestAssistantRoutes(t *testing.T) {
	h := newHandler(&fakeSource{}, static, nil, llm.New(llm.Config{}), foam.DefaultAudit())
	if w := get(h, "/api/llm/config"); w.Code != 200 || !strings.Contains(w.Body.String(), `"server":false`) {
		t.Fatalf("config: %d %q", w.Code, w.Body)
	}
	r := httptest.NewRequest("POST", "/api/llm/chat", strings.NewReader(`{"messages":[{"role":"user","content":"q"}]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "no server connection") {
		t.Fatalf("chat: %d %q", w.Code, w.Body)
	}
}

func TestAssistantFlags(t *testing.T) {
	cases := []struct {
		args []string
		ok   bool
	}{
		{[]string{"--llm-url", "https://api.openai.com/v1"}, false},
		{[]string{"--llm-url", "https://api.openai.com/v1", "--llm-model", "gpt-4.1-mini"}, true},
		{[]string{"--llm-max-tokens-per-question", "-1"}, false},
		{[]string{"--llm-model", "gpt-4.1-mini"}, false},
		{[]string{"--llm-url", "http://localhost:11434/v1", "--llm-model", "m"}, true},
	}
	for _, bad := range []string{"api.openai.com/v1", "ftp://api.openai.com/v1", "https:///v1", "https://user:pw@api.openai.com/v1",
		"https://api.openai.com/v1?x=1", "https://api.openai.com/v1?", "https://api.openai.com/v1#f"} {
		cases = append(cases, struct {
			args []string
			ok   bool
		}{[]string{"--llm-url", bad, "--llm-model", "m"}, false})
	}
	for _, tc := range cases {
		if _, err := parseFlags(tc.args); (err == nil) != tc.ok {
			t.Errorf("%v: err=%v", tc.args, err)
		}
	}
	o, _ := parseFlags([]string{"--llm-allowed-hosts", "api.openai.com, *.openai.azure.com"})
	if fmt.Sprint(o.llm.AllowedHosts) != "[api.openai.com *.openai.azure.com]" || o.llm.MaxTokens != 50000 || !o.llm.AllowAnyURL {
		t.Fatalf("defaults: %+v", o.llm)
	}
	for _, args := range [][]string{{"--host", "0.0.0.0", "--auth", "oidc"}, {"--host", "0.0.0.0", "--allow-unauthenticated"}} {
		if o, err := parseFlags(args); err != nil || o.llm.AllowAnyURL {
			t.Fatalf("%v: any URL allowed on a shared deployment (err=%v)", args, err)
		}
	}
	t.Setenv("K8SFOAMS_LLM_API_KEY", "env-key")
	if _, err := parseFlags([]string{"--llm-api-key-file", "/k"}); err == nil {
		t.Fatal("env key and key file together accepted")
	}
	if o, _ := parseFlags(nil); o.llm.Key != "env-key" {
		t.Fatal("env key not read")
	}
}

// rbacProblems lists what a manifest grants beyond this account's own
// read-only ClusterRole. roleRef names are read in block and flow style.
func rbacProblems(manifest string) []string {
	var rules strings.Builder
	for line := range strings.Lines(manifest) {
		code, _, _ := strings.Cut(line, "#")
		rules.WriteString(code)
	}
	var out []string
	for _, banned := range []string{"secrets", "configmaps", "pods/exec", "pods/attach", "pods/portforward",
		"create", "update", "patch", "delete", "escalate", "impersonate", "proxy", "aggregationRule", "*"} {
		if strings.Contains(rules.String(), banned) {
			out = append(out, "grants "+banned)
		}
	}
	own := regexp.MustCompile(`kind: ClusterRole\nmetadata:\n\s+name: (\S+)`).FindStringSubmatch(rules.String())
	if own == nil {
		return append(out, "no ClusterRole")
	}
	refs := regexp.MustCompile(`(?s)roleRef:\s*\{[^}]*?\bname:\s*["']?([^\s,}"']+)`).FindAllStringSubmatch(rules.String(), -1)
	refs = append(refs, regexp.MustCompile(`(?s)roleRef:[ \t]*\n.*?\bname:\s*["']?([^\s"']+)`).FindAllStringSubmatch(rules.String(), -1)...)
	if len(refs) == 0 {
		out = append(out, "no roleRef")
	}
	for _, ref := range refs {
		if ref[1] != own[1] {
			out = append(out, fmt.Sprintf("roleRef %q is not this repo's %q", ref[1], own[1]))
		}
	}
	return out
}

// The assistant can only reach what this account can read: keep Secrets,
// ConfigMaps and exec out of it, and keep it read-only.
func TestRBACCannotReadSecrets(t *testing.T) {
	b, err := os.ReadFile("deploy/base/rbac.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p := rbacProblems(string(b)); len(p) > 0 {
		t.Errorf("deploy/base/rbac.yaml: %v", p)
	}
	own := "kind: ClusterRole\nmetadata:\n  name: k8sfoams\nrules: []\n---\nroleRef:\n  name: k8sfoams\n"
	if p := rbacProblems(own); len(p) != 0 {
		t.Errorf("block-style own role flagged: %v", p)
	}
	for name, bad := range map[string]string{
		"flow":         own + "---\nroleRef: {kind: ClusterRole, name: cluster-admin}\n",
		"flow quoted":  own + "---\nroleRef: {apiGroup: x, name: \"cluster-admin\", kind: ClusterRole}\n",
		"block":        own + "---\nroleRef:\n  kind: ClusterRole\n  name: cluster-admin\n",
		"block quoted": own + "---\nroleRef:\n  name: 'cluster-admin'\n",
	} {
		if len(rbacProblems(bad)) == 0 {
			t.Errorf("%s-style cluster-admin binding passed", name)
		}
	}
}

// limitlessPod is one pod d/p with no memory limit, and an audit that turns
// that rule off, for checking every finding surface honours --audit-disable.
func limitlessPod() (*fakeSource, foam.Audit) {
	src := &fakeSource{
		nodes: []foam.Node{{Name: "n", CPU: 1000, Memory: 1_000_000_000}},
		pods: []foam.Pod{{Name: "p", Namespace: "d", NodeName: "n", CPU: 100, Memory: 100_000_000,
			Containers: []foam.Container{{Name: "c", CPU: 100, Memory: 100_000_000}}}},
	}
	off := foam.DefaultAudit()
	off.Disabled["missing-limits"] = true
	return src, off
}

func TestAuditConfigReachesRoutes(t *testing.T) {
	src, off := limitlessPod()
	for _, target := range []string{"/resources/cpu", "/report.json", "/report.csv"} {
		if b := get(newHandler(src, static, nil, nil, foam.DefaultAudit()), target).Body.String(); !strings.Contains(b, "missing-limits") {
			t.Errorf("%s default: missing-limits not reported: %s", target, b)
		}
		// The tree's trailing "audit" block names the disabled rule; only findings count.
		b, _, _ := strings.Cut(get(newHandler(src, static, nil, nil, off), target).Body.String(), `"audit":`)
		if strings.Contains(b, "missing-limits") {
			t.Errorf("%s disabled: missing-limits still reported: %s", target, b)
		}
	}
}
