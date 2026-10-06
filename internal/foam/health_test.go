package foam

import (
	"reflect"
	"slices"
	"testing"
)

func healthy(overrides map[string]bool) map[string]bool {
	c := map[string]bool{"MemoryPressure": false, "DiskPressure": false, "PIDPressure": false, "Ready": true}
	for k, v := range overrides {
		c[k] = v
	}
	return c
}

func TestWarnings(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want []string
	}{
		{"healthy", Node{Conditions: healthy(nil)}, []string{}},
		{"no metadata at all", Node{}, []string{}},
		{"cordoned", Node{Unschedulable: true, Conditions: healthy(nil)}, []string{"cordoned"}},
		{"memory pressure", Node{Conditions: healthy(map[string]bool{"MemoryPressure": true})}, []string{"memory-pressure"}},
		{"disk pressure", Node{Conditions: healthy(map[string]bool{"DiskPressure": true})}, []string{"disk-pressure"}},
		{"pid pressure", Node{Conditions: healthy(map[string]bool{"PIDPressure": true})}, []string{"pid-pressure"}},
		{"not ready", Node{Conditions: healthy(map[string]bool{"Ready": false})}, []string{"not-ready"}},
		{"missing Ready key", Node{Conditions: map[string]bool{"MemoryPressure": false}}, []string{}},
		{"NoSchedule", Node{Taints: []Taint{{Key: "gpu-only", Effect: "NoSchedule"}}}, []string{"tainted"}},
		{"NoExecute", Node{Taints: []Taint{{Key: "evict-me", Effect: "NoExecute"}}}, []string{"tainted"}},
		{"PreferNoSchedule alone", Node{Taints: []Taint{{Key: "spot", Effect: "PreferNoSchedule"}}}, []string{}},
		{"PreferNoSchedule + blocking", Node{Taints: []Taint{{Key: "spot", Effect: "PreferNoSchedule"}, {Key: "gpu", Effect: "NoSchedule"}}}, []string{"tainted"}},
		{"worst first", Node{
			Unschedulable: true,
			Taints:        []Taint{{Key: "gpu", Effect: "NoSchedule"}},
			Conditions:    healthy(map[string]bool{"Ready": false, "MemoryPressure": true, "DiskPressure": true}),
		}, []string{"cordoned", "not-ready", "memory-pressure", "disk-pressure", "tainted"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Warnings(tc.node); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// 4 cores / 16000 MB — a 1:4 core:GB node.
var worker = Node{Name: "worker", CPU: 4000, Memory: 16_000_000_000}

func auditPod(cpu int64, mem int64, limit bool, inits ...Container) Pod {
	c := Container{Name: "app", CPU: cpu, Memory: mem}
	if limit {
		one := int64(1)
		c.MemoryLimit = &one
	}
	return Pod{Name: "app", NodeName: "worker", CPU: cpu, Memory: mem, Containers: []Container{c}, InitContainers: inits}
}

// Containers set nothing; spec.resources at pod level bounds them all.
func podLevel(r Container) Pod {
	p := auditPod(0, 0, false)
	p.CPU, p.Memory, p.PodLevel = 400, 1_600_000_000, r
	return p
}

func TestFindings(t *testing.T) {
	cases := []struct {
		name string
		pod  Pod
		node Node
		want []string
	}{
		{"well sized", auditPod(400, 1_600_000_000, true), worker, []string{}},
		{"no cpu request", auditPod(0, 1_600_000_000, true), worker, []string{"missing-requests"}},
		{"no memory request", auditPod(400, 0, true), worker, []string{"missing-requests"}},
		{"no memory limit", auditPod(400, 1_600_000_000, false), worker, []string{"missing-limits"}},
		{"init containers not audited", auditPod(400, 1_600_000_000, true, Container{Name: "init"}), worker, []string{}},
		{"exactly 80% is fine", auditPod(3200, 12_800_000_000, true), worker, []string{}},
		{"lopsided", auditPod(2000, 800_000_000, true), worker, []string{"ratio-asymmetry"}},
		{"small lopsided", auditPod(200, 16_000_000, true), worker, []string{}},
		{"node without capacity", auditPod(400, 1_600_000_000, true), Node{Name: "ghost"}, []string{}},
		{"all in order", auditPod(3600, 1_600_000_000, false), worker, []string{"missing-limits", "monolith", "ratio-asymmetry"}},
		{"pod-level requests and limit", podLevel(Container{CPU: 400, Memory: 1_600_000_000, MemoryLimit: new(int64(1))}), worker, []string{}},
		{"pod-level cpu only", podLevel(Container{CPU: 400, MemoryLimit: new(int64(1))}), worker, []string{"missing-requests"}},
		{"pod-level requests, no limit", podLevel(Container{CPU: 400, Memory: 1_600_000_000}), worker, []string{"missing-limits"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Findings(tc.pod, tc.node, DefaultAudit); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, f := range Findings(tc.pod, tc.node, DefaultAudit) {
				if !slices.Contains(AuditRules, f) {
					t.Errorf("%q is emitted but missing from AuditRules", f)
				}
			}
		})
	}
	for _, p := range []Pod{auditPod(3600, 14_400_000_000, true), auditPod(3600, 13_000_000_000, true)} {
		if f := Findings(p, worker, DefaultAudit); !reflect.DeepEqual(f[:1], []string{"monolith"}) {
			t.Errorf("monolith expected in %v", f)
		}
	}
}

func TestCrashFindings(t *testing.T) {
	ok := Container{Name: "app", CPU: 100, Memory: 100, MemoryLimit: new(int64(200))}
	n := Node{CPU: 10_000, Memory: 10_000}
	cases := []struct {
		name     string
		statuses []ContainerStatus
		want     []string
	}{
		{"healthy", []ContainerStatus{{Name: "app", Ready: true}}, []string{}},
		{"no status yet", nil, []string{}},
		{"crash loop after OOM", []ContainerStatus{{Name: "app", Restarts: 3, Waiting: "CrashLoopBackOff", LastExitReason: "OOMKilled", LastExitCode: 137}}, []string{"crashloop", "oom-killed"}},
		{"OOM then recovered", []ContainerStatus{{Name: "app", Ready: true, Restarts: 1, LastExitReason: "OOMKilled"}}, []string{"oom-killed"}},
		{"image pull back-off", []ContainerStatus{{Name: "app", Waiting: "ImagePullBackOff"}}, []string{"image-pull"}},
		{"first pull error", []ContainerStatus{{Name: "app", Waiting: "ErrImagePull"}}, []string{"image-pull"}},
		{"sidecar crashing", []ContainerStatus{{Name: "app", Ready: true}, {Name: "proxy", Waiting: "CrashLoopBackOff"}}, []string{"crashloop"}},
		{"exit 1 is not OOM", []ContainerStatus{{Name: "app", Ready: true, LastExitReason: "Error", LastExitCode: 1}}, []string{}},
	}
	for _, tc := range cases {
		p := Pod{CPU: 100, Memory: 100, Containers: []Container{ok}, Statuses: tc.statuses}
		if got := Findings(p, n, DefaultAudit); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

func TestResizeFindings(t *testing.T) {
	p := auditPod(400, 1_600_000_000, true)
	for state, want := range map[string]string{"deferred": "resize-deferred", "infeasible": "resize-infeasible"} {
		p.Resize = &Resize{State: state}
		if got := Findings(p, worker, DefaultAudit); !reflect.DeepEqual(got, []string{want}) {
			t.Fatalf("%s: got %v", state, got)
		}
	}
}

func without(rules ...string) Audit {
	a := DefaultAudit
	a.Disabled = map[string]bool{}
	for _, r := range rules {
		a.Disabled[r] = true
	}
	return a
}

func TestAuditThresholds(t *testing.T) {
	custom := Audit{MonolithShare: 0.7, RatioFactor: 2, RatioMinShare: 0.05}
	cases := []struct {
		name string
		a    Audit
		pod  Pod
		want []string
	}{
		{"exactly 70% is fine", custom, auditPod(2800, 11_200_000_000, true), []string{}},
		{"just over 70%", custom, auditPod(2801, 11_200_000_000, true), []string{"monolith"}},
		{"exactly 2x is lopsided", custom, auditPod(800, 1_600_000_000, true), []string{"ratio-asymmetry"}},
		{"under 2x is fine", custom, auditPod(780, 1_600_000_000, true), []string{}},
		{"exactly at min share", custom, auditPod(200, 16_000_000, true), []string{"ratio-asymmetry"}},
		{"under min share", custom, auditPod(160, 16_000_000, true), []string{}},
		{"defaults unchanged", DefaultAudit, auditPod(2801, 11_200_000_000, true), []string{}},
		{"disabled rule dropped", without("missing-limits"), auditPod(3600, 1_600_000_000, false), []string{"monolith", "ratio-asymmetry"}},
		{"every rule disabled", without(AuditRules...), auditPod(3600, 0, false), []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Findings(tc.pod, worker, tc.a); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestAuditReachesTreemapAndReport(t *testing.T) {
	nodes, pods := []Node{worker}, []Pod{auditPod(400, 1_600_000_000, false)}
	if f := Treemap(nodes, pods, CPU, without("missing-limits")).Groups[0].Groups[0].(PodGroup).Findings; len(f) != 0 {
		t.Errorf("treemap findings: %v", f)
	}
	if f := Report(nodes, pods, without("missing-limits"))[0].Findings; len(f) != 0 {
		t.Errorf("report findings: %v", f)
	}
}
