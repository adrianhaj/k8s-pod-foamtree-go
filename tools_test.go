package main

import (
	"context"
	"encoding/json"
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
	if call("get_pod_logs", `{"namespace":"pay","name":"api"}`); src.logReq.Tail != 200 {
		t.Errorf("default tail %d", src.logReq.Tail)
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
