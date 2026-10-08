package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/kube"
)

// syntheticSource is a made-up cluster for UI work, demos and scale tests;
// it never talks to an API server. Every syntheticChurn it replaces 1 pod in
// 50, so refresh animations have something to show. Churn follows the clock,
// not the call count: one refresh asks twice (CPU, then memory) and both
// answers must list the same pods.
type syntheticSource struct {
	nodes, podsPerNode int
	now                func() time.Time
}

const syntheticChurn = 5 * time.Second

func parseSynthetic(spec string) (*syntheticSource, error) {
	var n, m int
	if _, err := fmt.Sscanf(spec, "%dx%d", &n, &m); err != nil || n < 1 || m < 1 || n > 200_000 || m > 200_000 || n*m > 200_000 {
		return nil, fmt.Errorf("--synthetic wants NODESxPODS_PER_NODE, at most 200000 pods, got %q", spec)
	}
	return &syntheticSource{nodes: n, podsPerNode: m, now: time.Now}, nil
}

func (s *syntheticSource) Contexts() ([]kube.Context, error) {
	return []kube.Context{{Context: "synthetic", Active: true}}, nil
}

func (s *syntheticSource) Started() []string { return []string{"synthetic"} }

// Logs makes up steady request lines. A previous run ends in an OOM, so the
// Logs tab and the assistant have a crash to look at.
func (s *syntheticSource) Logs(_ context.Context, _ string, req kube.LogRequest) (io.ReadCloser, error) {
	var b strings.Builder
	start := s.now().Add(-time.Duration(req.Tail) * time.Second).UTC()
	n := req.Tail
	if req.Previous {
		n -= 2
	}
	for i := range n {
		fmt.Fprintf(&b, "%s INFO  %s/%s handled request %d in %dms\n",
			start.Add(time.Duration(i)*time.Second).Format(time.RFC3339), req.Namespace, req.Pod, i, 3+i*7%90)
	}
	if req.Previous {
		end := start.Add(time.Duration(n) * time.Second).Format(time.RFC3339)
		fmt.Fprintf(&b, "%s ERROR runtime: out of memory: cannot allocate 33554432-byte block\n%s ERROR fatal error: out of memory\n", end, end)
	}
	return io.NopCloser(strings.NewReader(b.String())), nil
}

// Usage makes up readings: pods run from idle to 20% over their request, and
// one in seven has no metrics yet, so the overlay shows every case.
// It derives each pod from its index rather than building the whole cluster.
func (s *syntheticSource) Usage(context.Context, string) (map[string]kube.Usage, error) {
	gen := s.gen()
	total := s.nodes * s.podsPerNode
	out := make(map[string]kube.Usage, total)
	for k := range total {
		if k%7 == 6 {
			continue
		}
		name, ns, cpu, mem := syntheticPod(k, gen)
		f := float64(k%5) * 0.3
		out[ns+"/"+name] = kube.Usage{CPU: int64(float64(cpu) * f), Memory: int64(float64(mem) * f)}
	}
	return out, nil
}

// Quotas caps three namespaces from their own requests: team-0 is at its CPU
// quota, team-1 has room, team-2 is near its pod count.
func (s *syntheticSource) Quotas(ctx context.Context, name string) ([]kube.Quota, error) {
	nodes, pods, _ := s.Snapshot(ctx, name)
	// The Showback rows' own totals, so the Quota column matches the row beside it.
	rows, _ := foam.Showback(nodes, pods, "namespace")
	used := map[string]map[string]float64{}
	for _, r := range rows {
		used[r.Group] = map[string]float64{"requests.cpu": float64(r.CPU) / 1000, "requests.memory": float64(r.MemoryBytes), "pods": float64(r.Pods)}
	}
	scaled := func(u map[string]float64, f float64, keys ...string) map[string]float64 {
		h := map[string]float64{}
		for _, k := range keys {
			h[k] = u[k] * f
		}
		return h
	}
	q := func(ns, name string, f float64, keys ...string) kube.Quota {
		return kube.Quota{Namespace: ns, Name: name, Hard: scaled(used[ns], f, keys...), Used: scaled(used[ns], 1, keys...)}
	}
	return []kube.Quota{
		q("team-0", "compute", 1, "requests.cpu", "requests.memory"),
		q("team-1", "compute", 1.6, "requests.cpu", "requests.memory"),
		q("team-2", "compute", 1.3, "requests.cpu"),
		q("team-2", "objects", 1.05, "pods"),
	}, nil
}

func (s *syntheticSource) gen() int { return int(s.now().UnixNano() / int64(syntheticChurn)) }

// syntheticPod is what Snapshot and Usage agree on for pod k: name, namespace and requests.
func syntheticPod(k, gen int) (name, ns string, cpu, mem int64) {
	cpu, mem = int64(25+(k*37)%400), int64(50_000+(k*7919)%1_500_000)*1000
	if k%13 == 0 {
		cpu, mem = 0, 0
	}
	// Deployment-style names (svc05-<rs hash>-<suffix>) so the console groups
	// replicas into one workload across nodes. The hash changes for pod k once
	// every 50 generations; the suffix keeps names unique (7919 is coprime to 27^5).
	svc := k % 40
	return fmt.Sprintf("svc%02d-%s-%s", svc, randToken((k+gen)/50*104729+svc, 8), randToken(k*7919%14_348_907, 5)),
		fmt.Sprintf("team-%d", svc%9), cpu, mem
}

// randToken spells x in the alphabet Kubernetes uses for generated name parts.
func randToken(x, n int) string {
	const alphabet = "bcdfghjklmnpqrstvwxz2456789"
	b := make([]byte, n)
	for i := range b {
		b[i], x = alphabet[x%len(alphabet)], x/len(alphabet)
	}
	return string(b)
}

func (s *syntheticSource) Snapshot(context.Context, string) ([]foam.Node, []foam.Pod, error) {
	gen := s.gen()
	pools := []string{"general", "general", "memory", "spot"}
	nodes := make([]foam.Node, 0, s.nodes)
	pods := make([]foam.Pod, 0, s.nodes*s.podsPerNode+3)
	for i := range s.nodes {
		n := foam.Node{
			Name: fmt.Sprintf("node-%04d", i), CPU: 16_000, Memory: 64_000_000_000,
			Conditions: map[string]bool{"Ready": true, "MemoryPressure": i%31 == 5},
			Zone:       "synthetic-1" + string(rune('a'+i%3)), Region: "synthetic-1",
			InstanceType: "m.4xlarge", Pool: pools[i%len(pools)], Unschedulable: i%23 == 7,
			CapacityType: "on-demand",
			Extended:     map[string]int64{"ephemeral-storage": 100_000_000_000},
		}
		if i%8 == 1 {
			n.Pool = "gpu"
		}
		switch n.Pool {
		case "memory":
			n.Memory, n.InstanceType = 128_000_000_000, "r.4xlarge"
			n.Extended["hugepages-2Mi"] = 4 << 30
		case "spot":
			n.Taints = []foam.Taint{{Key: "spot", Value: "true", Effect: "NoSchedule"}}
			n.CapacityType = "spot"
		case "gpu":
			n.InstanceType = "g.4xlarge"
			n.Taints = []foam.Taint{{Key: "nvidia.com/gpu", Value: "present", Effect: "NoSchedule"}}
			n.Extended["nvidia.com/gpu"] = 8
		}
		n.AllocCPU, n.AllocMemory, n.AllocPods = n.CPU-500, n.Memory-2_000_000, 110
		n.Labels = map[string]string{"topology.kubernetes.io/zone": n.Zone, "karpenter.sh/nodepool": n.Pool}
		nodes = append(nodes, n)
		for j := range s.podsPerNode {
			k := i*s.podsPerNode + j
			name, ns, cpu, mem := syntheticPod(k, gen)
			qos := "Burstable"
			switch {
			case k%13 == 0:
				qos = "BestEffort"
			case k%5 == 0:
				qos = "Guaranteed"
			}
			c := foam.Container{Name: "app", CPU: cpu, Memory: mem}
			p := foam.Pod{
				Name: name, Namespace: ns, NodeName: n.Name, CPU: cpu, Memory: mem,
				Labels: map[string]string{"app": fmt.Sprintf("svc%02d", k%40)}, QOS: qos,
			}
			switch qos {
			case "Guaranteed":
				cpuLim, memLim := cpu, mem
				c.MemoryLimit, p.MemoryLimit, p.CPULimit = &memLim, &memLim, &cpuLim
			case "Burstable":
				if k%11 != 0 {
					lim := mem * 3 / 2
					c.MemoryLimit, p.MemoryLimit = &lim, &lim
				}
				if k%3 == 0 {
					lim := cpu * 2
					p.CPULimit = &lim
				}
				// Only here: an init container without limits makes any pod Burstable.
				if k%17 == 0 {
					p.InitContainers = []foam.Container{{Name: "init", CPU: 100, Memory: 10_000_000}}
				}
			}
			ext := map[string]int64{}
			if k%4 == 0 {
				ext["ephemeral-storage"] = int64(1+k%5) << 30
			}
			// 6 of 8 GPUs and 2 of 4 GiB hugepages taken: free capacity to see.
			if n.Pool == "gpu" && j%3 == 0 && j < 12 {
				ext["nvidia.com/gpu"] = int64(1 + j%2)
			}
			if n.Pool == "memory" && j%5 == 0 && j < 20 {
				ext["hugepages-2Mi"] = 512 << 20
			}
			if len(ext) > 0 {
				c.Extended, p.Extended = ext, ext
			}
			switch n.Pool {
			case "memory":
				p.NodeSelector = map[string]string{"karpenter.sh/nodepool": "memory"}
			case "spot":
				p.Tolerations = []corev1.Toleration{{Key: "spot", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}}
			}
			switch {
			case j == 0:
				p.Controller = "DaemonSet"
			case k%29 != 3:
				p.Controller = "ReplicaSet"
			}
			st := foam.ContainerStatus{Name: "app", Ready: true}
			p.Phase = "Running"
			switch {
			case k%37 == 4:
				st = foam.ContainerStatus{Name: "app", Restarts: 14, Waiting: "CrashLoopBackOff", LastExitReason: "OOMKilled", LastExitCode: 137}
			case k%53 == 9:
				st, p.Phase = foam.ContainerStatus{Name: "app", Waiting: "ImagePullBackOff"}, "Pending"
			}
			p.Statuses = []foam.ContainerStatus{st}
			if k%19 == 2 {
				p.Throttled = 0.4
			}
			// svc00–04 scale on an HPA, svc05–09 carry a VPA recommendation below their requests.
			switch svc := k % 40; {
			case svc < 5:
				p.HPA = fmt.Sprintf("svc%02d", svc)
			case svc < 10:
				p.VPA = &foam.Container{CPU: cpu * 6 / 10, Memory: mem * 8 / 10}
			}
			if k%23 == 1 {
				p.LimitRange = "cpu, memory request for container app"
			}
			p.Containers = []foam.Container{c}
			pods = append(pods, p)
		}
	}
	// Bigger than any node, so the Pending tab always has something to show.
	for i := range 3 {
		c := foam.Container{Name: "job", CPU: 64_000, Memory: 1_000_000_000}
		pods = append(pods, foam.Pod{
			Name: fmt.Sprintf("batch-%d", i), Namespace: "team-0", CPU: c.CPU, Memory: c.Memory,
			Containers: []foam.Container{c}, QOS: "Burstable", Controller: "Job",
			Phase:        "Pending",
			SchedReason:  "Unschedulable",
			SchedMessage: fmt.Sprintf("0/%d nodes are available: %d Insufficient cpu.", s.nodes, s.nodes),
		})
	}
	return nodes, pods, nil
}
