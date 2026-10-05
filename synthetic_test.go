package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
)

func TestParseSynthetic(t *testing.T) {
	for _, bad := range []string{"", "x", "abc", "0x5", "5x0", "-1x5", "1000x1000", "4294967296x4294967296"} {
		if _, err := parseSynthetic(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if s, err := parseSynthetic("100x50"); err != nil || s.nodes != 100 || s.podsPerNode != 50 {
		t.Fatalf("got %+v %v", s, err)
	}
}

func TestSyntheticSnapshot(t *testing.T) {
	s, _ := parseSynthetic("30x20")
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return clock }
	nodes, pods, err := s.Snapshot(context.Background(), "")
	if err != nil || len(nodes) != 30 || len(pods) != 600 {
		t.Fatalf("nodes=%d pods=%d err=%v", len(nodes), len(pods), err)
	}
	zones, pools, capacity := map[string]bool{}, map[string]bool{}, map[string]bool{}
	requested := map[string]int64{}
	for _, p := range pods {
		requested[p.NodeName] += p.Memory
	}
	for _, n := range nodes {
		zones[n.Zone], pools[n.Pool], capacity[n.CapacityType] = true, true, true
		// Catches node and pod memory drifting into different units.
		if requested[n.Name] > n.Memory {
			t.Fatalf("%s: pods request %d bytes of memory, node has %d", n.Name, requested[n.Name], n.Memory)
		}
	}
	unboundedMem, boundedCPU := 0, 0
	for _, p := range pods {
		if p.MemoryLimit == nil {
			unboundedMem++
		}
		if p.CPULimit != nil {
			boundedCPU++
		}
	}
	if len(zones) != 3 || len(pools) < 3 || len(capacity) != 2 || !capacity["spot"] || !capacity["on-demand"] ||
		unboundedMem == 0 || boundedCPU == 0 || boundedCPU == len(pods) {
		t.Fatalf("not varied enough: zones=%v pools=%v capacity=%v unboundedMem=%d boundedCPU=%d", zones, pools, capacity, unboundedMem, boundedCPU)
	}

	// The CPU and memory requests of one refresh must see the same pods.
	_, same, _ := s.Snapshot(context.Background(), "")
	for i := range pods {
		if pods[i].Name != same[i].Name {
			t.Fatalf("pods changed within one churn window: %s vs %s", pods[i].Name, same[i].Name)
		}
	}

	clock = clock.Add(syntheticChurn)
	_, next, _ := s.Snapshot(context.Background(), "")
	names := map[string]bool{}
	for _, p := range next {
		names[p.Name] = true
	}
	replaced := 0
	for _, p := range pods {
		if !names[p.Name] {
			replaced++
		}
	}
	if replaced != len(pods)/50 {
		t.Fatalf("replaced %d pods between refreshes, want %d", replaced, len(pods)/50)
	}
}

func TestSyntheticFlag(t *testing.T) {
	o, err := parseFlags([]string{"--synthetic", "10x5"})
	if err != nil || o.synthetic == nil || o.synthetic.nodes != 10 {
		t.Fatalf("got %+v %v", o.synthetic, err)
	}
	if _, err := parseFlags([]string{"--synthetic", "lots"}); err == nil {
		t.Fatal("bad --synthetic accepted")
	}
}

// Every QoS class shows up, and each pod's requests and limits agree with its
// class, so Color by → QoS and the eviction panel have real data to show.
func TestSyntheticQoS(t *testing.T) {
	s, _ := parseSynthetic("30x20")
	_, pods, _ := s.Snapshot(context.Background(), "")
	count := map[string]int{}
	for _, p := range pods {
		count[p.QOS]++
		switch p.QOS {
		case "BestEffort":
			if p.CPU != 0 || p.Memory != 0 || p.CPULimit != nil || p.MemoryLimit != nil || len(p.InitContainers) > 0 {
				t.Fatalf("BestEffort pod with requests or limits: %+v", p)
			}
		case "Guaranteed":
			if p.CPULimit == nil || *p.CPULimit != p.CPU || p.MemoryLimit == nil || *p.MemoryLimit != p.Memory || len(p.InitContainers) > 0 {
				t.Fatalf("Guaranteed pod with limits != requests: %+v", p)
			}
		}
	}
	if len(count) != 3 || count["BestEffort"] == 0 || count["Guaranteed"] == 0 || count["Burstable"] == 0 {
		t.Fatalf("QoS classes: %v", count)
	}
}

func TestSyntheticExtendedResources(t *testing.T) {
	s, _ := parseSynthetic("30x20")
	nodes, pods, _ := s.Snapshot(context.Background(), "")
	used := map[string]map[string]int64{}
	for _, p := range pods {
		if used[p.NodeName] == nil {
			used[p.NodeName] = map[string]int64{}
		}
		for k, v := range p.Extended {
			used[p.NodeName][k] += v
		}
	}
	offered := map[string]int{}
	for _, n := range nodes {
		for k, capacity := range n.Extended {
			offered[k]++
			if used[n.Name][k] > capacity {
				t.Errorf("%s: %s requests %d > capacity %d", n.Name, k, used[n.Name][k], capacity)
			}
		}
		for k := range used[n.Name] {
			if _, ok := n.Extended[k]; !ok {
				t.Errorf("%s: pods request %s the node does not offer", n.Name, k)
			}
		}
	}
	if offered["ephemeral-storage"] != 30 || offered["nvidia.com/gpu"] == 0 || offered["hugepages-2Mi"] == 0 {
		t.Fatalf("not varied enough: %v", offered)
	}
}

// The simulators need the constraints a real cluster has, or every drain fits.
func TestSyntheticSchedulingConstraints(t *testing.T) {
	s, _ := parseSynthetic("30x20")
	nodes, pods, _ := s.Snapshot(context.Background(), "")
	for _, n := range nodes {
		if n.AllocCPU == 0 || n.AllocCPU >= n.CPU || n.AllocPods != 110 || n.Labels["karpenter.sh/nodepool"] != n.Pool {
			t.Fatalf("node %+v", n)
		}
	}
	pool := map[string]string{}
	for _, n := range nodes {
		pool[n.Name] = n.Pool
	}
	kinds := map[string]int{}
	for _, p := range pods {
		kinds[p.Controller]++
		switch pool[p.NodeName] {
		case "spot":
			if len(p.Tolerations) != 1 {
				t.Fatalf("pod on a spot node without a toleration: %+v", p)
			}
		case "memory":
			if p.NodeSelector["karpenter.sh/nodepool"] != "memory" {
				t.Fatalf("pod on a memory node without a selector: %+v", p)
			}
		}
	}
	if kinds["DaemonSet"] != 30 || kinds[""] == 0 || kinds["ReplicaSet"] == 0 {
		t.Fatalf("controllers: %v", kinds)
	}
}

func TestSyntheticLogs(t *testing.T) {
	s, _ := parseSynthetic("2x2")
	rc, err := s.Logs(context.Background(), "", kube.LogRequest{Namespace: "team-1", Pod: "svc01-1-0", Tail: 20})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 20 || !strings.Contains(lines[0], "team-1/svc01-1-0") {
		t.Fatalf("%d lines, first %q", len(lines), lines[0])
	}
	rc, _ = s.Logs(context.Background(), "", kube.LogRequest{Namespace: "ns", Pod: "p", Tail: 5, Previous: true})
	b, _ = io.ReadAll(rc)
	if !strings.Contains(string(b), "out of memory") {
		t.Fatalf("previous run should end in an OOM: %q", b)
	}
}

func TestSyntheticCrashes(t *testing.T) {
	s, _ := parseSynthetic("20x20")
	_, pods, _ := s.Snapshot(context.Background(), "")
	seen := map[string]int{}
	for _, p := range pods {
		for _, f := range foam.Findings(p, foam.Node{CPU: 16_000, Memory: 64_000_000_000}) {
			seen[f]++
		}
	}
	if seen["crashloop"] == 0 || seen["oom-killed"] == 0 || seen["image-pull"] == 0 {
		t.Fatalf("synthetic cluster should show every crash finding: %v", seen)
	}
}
