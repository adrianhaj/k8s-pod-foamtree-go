package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/llm"
)

// clusterTools are the assistant's read-only views of one cluster, built on
// the same snapshot and simulators as the dashboard. Nothing here writes.
// One value serves one question, whose lookups run one at a time, so the
// snapshot is taken once, on the first lookup that needs it.
type clusterTools struct {
	src     source
	cluster string
	nodes   []foam.Node
	pods    []foam.Pod
	taken   bool
}

const (
	maxToolRows     = 100
	defaultToolTail = 200
	maxToolTail     = 500
	maxToolLogBytes = 32 << 10
)

var (
	noArgs   = json.RawMessage(`{"type":"object","properties":{}}`)
	nodeArgs = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	podArgs  = json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"name":{"type":"string"}},"required":["namespace","name"]}`)
)

var assistantTools = []llm.Tool{
	{Name: "cluster_summary", Description: "Node and pod counts, and requested CPU and memory against capacity.", Parameters: noArgs},
	{Name: "list_problems", Description: "Node warnings and pod findings, crashes and not-ready or cordoned nodes first, at most 100.", Parameters: noArgs},
	{Name: "describe_pod", Description: "One pod: node, phase, QoS, requests, limits, container status and findings.", Parameters: podArgs},
	{Name: "describe_node", Description: "One node: capacity, allocatable, requested totals, warnings and taints.", Parameters: nodeArgs},
	{Name: "get_pod_logs", Description: "Last lines of one container's logs. previous=true reads the run before the last restart, where a crash shows.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"namespace":{"type":"string"},"name":{"type":"string"},"container":{"type":"string"},"tail":{"type":"integer","maximum":` + strconv.Itoa(maxToolTail) + `},"previous":{"type":"boolean"}},"required":["namespace","name"]}`)},
	{Name: "fit_pod", Description: "Which nodes could schedule a pod with these requests, and why the others refuse.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"cpu":{"type":"string","description":"e.g. 500m"},"memory":{"type":"string","description":"e.g. 1Gi"},"nodeSelector":{"type":"string","description":"k=v,k2=v2"},"tolerations":{"type":"string","description":"key=value:Effect,..."}},"required":["cpu","memory"]}`)},
	{Name: "drain_node", Description: "Dry run: where each pod on the node would land if it were drained.", Parameters: nodeArgs},
}

func (clusterTools) Tools() []llm.Tool { return assistantTools }

func (t *clusterTools) Call(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	if !slices.ContainsFunc(assistantTools, func(tool llm.Tool) bool { return tool.Name == name }) {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	var a struct {
		Namespace, Name, Container             string
		CPU, Memory, NodeSelector, Tolerations string
		Tail                                   int64
		Previous                               bool
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("arguments are not valid JSON: %w", err)
		}
	}
	if name == "get_pod_logs" {
		return t.logs(ctx, kube.LogRequest{Namespace: a.Namespace, Pod: a.Name, Container: a.Container, Tail: a.Tail, Previous: a.Previous})
	}
	if !t.taken {
		nodes, pods, err := t.src.Snapshot(ctx, t.cluster)
		if err != nil {
			return "", err
		}
		t.nodes, t.pods, t.taken = nodes, pods, true
	}
	nodes, pods := t.nodes, t.pods
	var v any
	switch name {
	case "cluster_summary":
		v = summarize(nodes, pods)
	case "list_problems":
		v = problems(nodes, pods)
	case "describe_pod":
		i := slices.IndexFunc(pods, func(p foam.Pod) bool { return p.Namespace == a.Namespace && p.Name == a.Name })
		if i < 0 {
			return "", fmt.Errorf("no pod %s/%s in this cluster", a.Namespace, a.Name)
		}
		n, _ := nodeNamed(nodes, pods[i].NodeName)
		v = describePod(pods[i], n)
	case "describe_node":
		n, ok := nodeNamed(nodes, a.Name)
		if !ok {
			return "", fmt.Errorf("no node %q in this cluster", a.Name)
		}
		v = describeNode(n, pods)
	case "fit_pod":
		p, err := foam.Hypothetical(a.CPU, a.Memory, a.NodeSelector, a.Tolerations)
		if err != nil {
			return "", err
		}
		v = fitRows(foam.Fit(nodes, pods, p))
	case "drain_node":
		d, ok := foam.Drain(nodes, pods, a.Name)
		if !ok {
			return "", fmt.Errorf("no node %q in this cluster", a.Name)
		}
		v = d
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func (t *clusterTools) logs(ctx context.Context, req kube.LogRequest) (string, error) {
	if req.Tail < 1 {
		req.Tail = defaultToolTail
	}
	req.Tail = min(req.Tail, maxToolTail)
	if err := checkLogRequest(req); err != nil {
		return "", err
	}
	rc, err := t.src.Logs(ctx, t.cluster, req)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	// The crash is at the end, so keep the newest bytes.
	return string(kube.LastBytes(b, maxToolLogBytes)), nil
}

func summarize(nodes []foam.Node, pods []foam.Pod) map[string]any {
	var cpu, mem, cpuCap, memCap int64
	for _, n := range nodes {
		cpuCap, memCap = cpuCap+n.CPU, memCap+n.Memory
	}
	for _, p := range pods {
		cpu, mem = cpu+p.CPU, mem+p.Memory
	}
	return map[string]any{"nodes": len(nodes), "pods": len(pods),
		"cpuRequestedMillicores": cpu, "cpuCapacityMillicores": cpuCap,
		"memoryRequestedBytes": mem, "memoryCapacityBytes": memCap}
}

type problem struct {
	Object string `json:"object"`
	Node   string `json:"node"`
	Rule   string `json:"rule"`
}

// Severity lives in the UI's vocabulary; here only the urgent ones move up,
// so a cap of 100 rows never hides a crash behind missing limits.
var urgency = map[string]int{"crashloop": 0, "oom-killed": 0, "not-ready": 0, "cordoned": 0,
	"image-pull": 1, "memory-pressure": 1, "disk-pressure": 1, "pid-pressure": 1}

func problems(nodes []foam.Node, pods []foam.Pod) []problem {
	out := []problem{}
	byName := map[string]foam.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
		for _, w := range foam.Warnings(n) {
			out = append(out, problem{"node/" + n.Name, n.Name, w})
		}
	}
	for _, p := range pods {
		for _, f := range foam.Findings(p, byName[p.NodeName]) {
			out = append(out, problem{p.Namespace + "/" + p.Name, p.NodeName, f})
		}
	}
	rank := func(r string) int {
		if u, ok := urgency[r]; ok {
			return u
		}
		return 2
	}
	slices.SortStableFunc(out, func(a, b problem) int { return cmp.Compare(rank(a.Rule), rank(b.Rule)) })
	return out[:min(len(out), maxToolRows)]
}

func nodeNamed(nodes []foam.Node, name string) (foam.Node, bool) {
	i := slices.IndexFunc(nodes, func(n foam.Node) bool { return n.Name == name })
	if i < 0 {
		return foam.Node{}, false
	}
	return nodes[i], true
}

func describePod(p foam.Pod, n foam.Node) map[string]any {
	return map[string]any{"namespace": p.Namespace, "name": p.Name, "node": p.NodeName, "phase": p.Phase,
		"schedulingReason": p.SchedReason, "schedulingMessage": p.SchedMessage,
		"qos": p.QOS, "controller": p.Controller, "labels": p.Labels,
		"cpuRequestMillicores": p.CPU, "memoryRequestBytes": p.Memory,
		"cpuLimitMillicores": p.CPULimit, "memoryLimitBytes": p.MemoryLimit,
		"containers": containers(p.Containers), "statuses": p.Statuses, "findings": foam.Findings(p, n)}
}

// container is foam.Container with its units in the keys, so the model does not guess them.
type container struct {
	Name        string           `json:"name"`
	CPU         int64            `json:"cpuRequestMillicores"`
	Memory      int64            `json:"memoryRequestBytes"`
	MemoryLimit *int64           `json:"memoryLimitBytes"`
	Extended    map[string]int64 `json:"extendedRequests,omitempty"`
}

func containers(cs []foam.Container) []container {
	out := make([]container, 0, len(cs))
	for _, c := range cs {
		out = append(out, container(c))
	}
	return out
}

// fitRows puts the nodes that fit first and keeps at most maxToolRows.
func fitRows(vs []foam.Verdict) map[string]any {
	slices.SortStableFunc(vs, func(a, b foam.Verdict) int { return cmp.Compare(min(len(a.Reasons), 1), min(len(b.Reasons), 1)) })
	n := min(len(vs), maxToolRows)
	return map[string]any{"nodes": vs[:n], "omitted": len(vs) - n}
}

func describeNode(n foam.Node, pods []foam.Pod) map[string]any {
	var cpu, mem int64
	count := 0
	for _, p := range pods {
		if p.NodeName == n.Name {
			cpu, mem, count = cpu+p.CPU, mem+p.Memory, count+1
		}
	}
	return map[string]any{"name": n.Name, "zone": n.Zone, "pool": n.Pool, "instanceType": n.InstanceType,
		"capacityType": n.CapacityType, "cpuCapacityMillicores": n.CPU, "memoryCapacityBytes": n.Memory,
		"cpuAllocatableMillicores": n.AllocCPU, "memoryAllocatableBytes": n.AllocMemory,
		"cpuRequestedMillicores": cpu, "memoryRequestedBytes": mem, "pods": count,
		"warnings": foam.Warnings(n), "taints": n.Taints}
}
