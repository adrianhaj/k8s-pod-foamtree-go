package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
)

func TestClusterTools(t *testing.T) {
	src := &fakeSource{
		nodes: []foam.Node{{Name: "n1", CPU: 4000, Memory: 8 << 30, Conditions: map[string]bool{"Ready": true}}},
		pods: []foam.Pod{{Name: "api", Namespace: "pay", NodeName: "n1", CPU: 500, Memory: 1 << 30,
			Containers: []foam.Container{{Name: "api", CPU: 500, Memory: 1 << 30}},
			Statuses:   []foam.ContainerStatus{{Name: "api", Restarts: 3, Waiting: "CrashLoopBackOff"}}}},
		logs: "boom\n",
	}
	tools := clusterTools{src: src, cluster: "kind"}
	call := func(name, args string) string {
		t.Helper()
		out, err := tools.Call(context.Background(), name, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	if out := call("describe_pod", `{"namespace":"pay","name":"api"}`); !strings.Contains(out, "CrashLoopBackOff") || !strings.Contains(out, `"crashloop"`) {
		t.Errorf("describe_pod: %s", out)
	}
	if out := call("list_problems", ""); !strings.HasPrefix(out, `[{"object":"pay/api","node":"n1","rule":"crashloop"}`) {
		t.Errorf("list_problems should put crashes first: %s", out)
	}
	if out := call("cluster_summary", "{}"); !strings.Contains(out, `"nodes":1`) || !strings.Contains(out, `"pods":1`) {
		t.Errorf("cluster_summary: %s", out)
	}
	if out := call("describe_node", `{"name":"n1"}`); !strings.Contains(out, `"pods":1`) {
		t.Errorf("describe_node: %s", out)
	}
	if out := call("get_pod_logs", `{"namespace":"pay","name":"api","tail":9000,"previous":true}`); out != "boom\n" ||
		src.logReq.Tail != 500 || !src.logReq.Previous || src.asked != "kind" {
		t.Errorf("get_pod_logs: %q %+v on %q", out, src.logReq, src.asked)
	}
	for _, tail := range []string{``, `,"tail":0`, `,"tail":-5`} {
		if call("get_pod_logs", `{"namespace":"pay","name":"api"`+tail+`}`); src.logReq.Tail != 200 {
			t.Errorf("tail %q: got %d, want the default 200", tail, src.logReq.Tail)
		}
	}
	if out := call("fit_pod", `{"cpu":"500m","memory":"1Gi"}`); !strings.Contains(out, `"node":"n1"`) {
		t.Errorf("fit_pod: %s", out)
	}
	if out := call("drain_node", `{"name":"n1"}`); !strings.Contains(out, `"moved"`) {
		t.Errorf("drain_node: %s", out)
	}
	for name, args := range map[string]string{
		"describe_pod":    `{"namespace":"pay","name":"gone"}`,
		"describe_node":   `{"name":"gone"}`,
		"get_pod_logs":    `{"namespace":"../x","name":"api"}`,
		"fit_pod":         `{"cpu":"lots","memory":"1Gi"}`,
		"nope":            `{}`,
		"cluster_summary": `{`,
	} {
		if _, err := tools.Call(context.Background(), name, json.RawMessage(args)); err == nil {
			t.Errorf("%s %s: no error", name, args)
		}
	}
}

func TestPodLogsKeepTheNewestLines(t *testing.T) {
	var b strings.Builder
	b.WriteString("first\n")
	for b.Len() < 3*maxToolLogBytes {
		b.WriteString("line " + strings.Repeat("x", 40) + "\n")
	}
	b.WriteString("the crash\n")
	tools := clusterTools{src: &fakeSource{logs: b.String()}, cluster: "kind"}
	out, err := tools.Call(context.Background(), "get_pod_logs", json.RawMessage(`{"namespace":"pay","name":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxToolLogBytes || !strings.HasSuffix(out, "the crash\n") || strings.Contains(out, "first") || !strings.HasPrefix(out, "line ") {
		t.Errorf("want the last <=32 KiB from a line start, got %d bytes starting %q", len(out), out[:20])
	}
}

func TestPodLogsKeepAnOverlongLastLine(t *testing.T) {
	long := strings.Repeat("y", maxToolLogBytes+100) + "END\n"
	out := logsTool(t, "first\n"+long)
	if out == "" || len(out) != maxToolLogBytes || !strings.HasSuffix(out, "yEND\n") {
		t.Errorf("want the newest %d bytes of the last line, got %d bytes", maxToolLogBytes, len(out))
	}
}

func TestPodLogsCutAtALineBoundary(t *testing.T) {
	newest := strings.Repeat(strings.Repeat("x", 1023)+"\n", maxToolLogBytes/1024)
	if out := logsTool(t, "old line\n"+newest); out != newest {
		t.Errorf("want exactly the newest %d bytes, got %d bytes", len(newest), len(out))
	}
}

func logsTool(t *testing.T, logs string) string {
	t.Helper()
	tools := clusterTools{src: &fakeSource{logs: logs}, cluster: "kind"}
	out, err := tools.Call(context.Background(), "get_pod_logs", json.RawMessage(`{"namespace":"pay","name":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFitPodListsFittingNodesFirstAndCapsRows(t *testing.T) {
	var nodes []foam.Node
	for i := range 100 {
		nodes = append(nodes, foam.Node{Name: fmt.Sprintf("a%03d", i)}) // no room: refuses
	}
	for i := range 50 {
		nodes = append(nodes, foam.Node{Name: fmt.Sprintf("b%03d", i), AllocCPU: 4000, AllocMemory: 8 << 30, AllocPods: 110})
	}
	tools := clusterTools{src: &fakeSource{nodes: nodes}, cluster: "kind"}
	out, err := tools.Call(context.Background(), "fit_pod", json.RawMessage(`{"cpu":"500m","memory":"1Gi"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Nodes   []foam.Verdict
		Omitted int
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != maxToolRows || got.Omitted != 50 || len(got.Nodes[49].Reasons) != 0 || len(got.Nodes[50].Reasons) == 0 {
		t.Fatalf("%d rows, %d omitted, row 49 %+v, row 50 %+v", len(got.Nodes), got.Omitted, got.Nodes[49], got.Nodes[50])
	}
}

func TestDescribePodNamesContainerUnits(t *testing.T) {
	limit := int64(2 << 30)
	src := &fakeSource{pods: []foam.Pod{{Name: "api", Namespace: "pay",
		Containers: []foam.Container{{Name: "api", CPU: 500, Memory: 1 << 30, MemoryLimit: &limit}}}}}
	out, err := (clusterTools{src: src}).Call(context.Background(), "describe_pod", json.RawMessage(`{"namespace":"pay","name":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `"containers":[{"name":"api","cpuRequestMillicores":500,"memoryRequestBytes":1073741824,"memoryLimitBytes":2147483648}]`
	if !strings.Contains(out, want) {
		t.Fatalf("%s", out)
	}
}

// Every tool is a read path the model can pull cluster data through. A new
// one needs a review that it cannot reach Secrets, ConfigMaps or env values.
func TestToolsAreTheReviewedReadOnlySet(t *testing.T) {
	var names []string
	for _, tool := range (clusterTools{}).Tools() {
		names = append(names, tool.Name)
	}
	want := "cluster_summary list_problems describe_pod describe_node get_pod_logs fit_pod drain_node"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("tool set changed to %q: review it for secret access, then update this test", got)
	}
}

func TestToolSchemasAreValidJSON(t *testing.T) {
	for _, tool := range (clusterTools{}).Tools() {
		var v map[string]any
		if err := json.Unmarshal(tool.Parameters, &v); err != nil || v["type"] != "object" {
			t.Errorf("%s: %v", tool.Name, err)
		}
	}
}
