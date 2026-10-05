package foam

var pressureSlugs = map[string]string{
	"MemoryPressure": "memory-pressure",
	"DiskPressure":   "disk-pressure",
	"PIDPressure":    "pid-pressure",
}

// Warnings lists why a node is a bad scheduling target, worst first, as
// render-ready slugs so the 2D and 3D views cannot disagree.
func Warnings(n Node) []string {
	w := []string{}
	if n.Unschedulable {
		w = append(w, "cordoned")
	}
	if ready, ok := n.Conditions["Ready"]; ok && !ready {
		w = append(w, "not-ready")
	}
	for _, name := range pressureConditions {
		if n.Conditions[name] {
			w = append(w, pressureSlugs[name])
		}
	}
	// PreferNoSchedule is a soft hint: listed in the detail view, never a mark.
	for _, t := range n.Taints {
		if t.Effect == "NoSchedule" || t.Effect == "NoExecute" {
			w = append(w, "tainted")
			break
		}
	}
	return w
}

const (
	// A pod reserving more than this share of a node cannot be rescheduled
	// anywhere else, and a drain takes the whole workload down with it.
	monolithShare = 0.8
	// CPU and memory shares this far apart strand the other axis's capacity.
	ratioAsymmetryFactor = 4
	// Below this dominant share a pod is too small to strand anything.
	ratioMinShare = 0.10
)

func share(requested, capacity float64) float64 {
	if capacity == 0 {
		return 0
	}
	return requested / capacity
}

// Findings lists a pod's best-practice violations on its node, in fixed order.
// Init containers are skipped: they finish before the pod runs. Pod-level
// resources cover every container that sets none of its own.
func Findings(p Pod, n Node) []string {
	f := []string{}
	missingRequests, missingLimits := false, false
	for _, c := range p.Containers {
		missingRequests = missingRequests || (c.CPU == 0 && p.PodLevel.CPU == 0) || (c.Memory == 0 && p.PodLevel.Memory == 0)
		missingLimits = missingLimits || (c.MemoryLimit == nil && p.PodLevel.MemoryLimit == nil)
	}
	if missingRequests {
		f = append(f, "missing-requests")
	}
	if missingLimits {
		f = append(f, "missing-limits")
	}

	cpu := share(float64(p.CPU), float64(n.CPU))
	mem := share(float64(p.Memory), float64(n.Memory))
	if cpu > monolithShare || mem > monolithShare {
		f = append(f, "monolith")
	}
	// A zero on either axis is already missing-requests and has no ratio.
	if cpu > 0 && mem > 0 {
		high, low := max(cpu, mem), min(cpu, mem)
		if high >= ratioMinShare && high/low >= ratioAsymmetryFactor {
			f = append(f, "ratio-asymmetry")
		}
	}

	crash, oom, pull := false, false, false
	for _, s := range p.Statuses {
		crash = crash || s.Waiting == "CrashLoopBackOff"
		oom = oom || s.LastExitReason == "OOMKilled"
		pull = pull || s.Waiting == "ImagePullBackOff" || s.Waiting == "ErrImagePull"
	}
	if crash {
		f = append(f, "crashloop")
	}
	if oom {
		f = append(f, "oom-killed")
	}
	if pull {
		f = append(f, "image-pull")
	}
	if p.Resize != nil {
		f = append(f, "resize-"+p.Resize.State)
	}
	return f
}
