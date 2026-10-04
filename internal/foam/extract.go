// Package foam turns Kubernetes nodes and pods into the treemap JSON the
// dashboard renders. It is pure: no client-go calls, only API types in.
package foam

import (
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	resourcehelper "k8s.io/component-helpers/resource"
)

// ContainerStatus is what the crash findings and the Logs tab read; the
// API's free-text messages are left behind.
type ContainerStatus struct {
	Name           string `json:"name"`
	Ready          bool   `json:"ready"`
	Restarts       int32  `json:"restarts"`
	Waiting        string `json:"waiting,omitempty"`
	LastExitReason string `json:"lastExitReason,omitempty"`
	LastExitCode   int32  `json:"lastExitCode,omitempty"`
}

// CPU is in millicores, memory in bytes — the scheduler's own units.
type Container struct {
	Name   string
	CPU    int64
	Memory int64
	// nil when unset: an unbounded container is exactly what the audit flags.
	MemoryLimit *int64
	Extended    map[string]int64
}

type Pod struct {
	Name, NodeName, Namespace string
	// Effective request — what the scheduler reserves for the pod.
	CPU            int64
	Memory         int64
	Containers     []Container
	InitContainers []Container
	Labels         map[string]string
	QOS            string
	// spec.resources, which bounds all containers at once; zero when unset.
	PodLevel Container
	// nil when any running container is unbounded on that axis.
	CPULimit    *int64
	MemoryLimit *int64
	Extended    map[string]int64
	// What the scheduler's filters read. Affinity holds node affinity only.
	NodeSelector map[string]string
	Affinity     *corev1.Affinity
	Tolerations  []corev1.Toleration
	// Kind of the controlling owner, "" for a bare pod: decides what a drain does with it.
	Controller string
	Phase      string
	Statuses   []ContainerStatus
}

type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type Node struct {
	Name          string
	CPU           int64
	Memory        int64
	Unschedulable bool
	Taints        []Taint
	Conditions    map[string]bool
	// "" when the node does not carry the label.
	Zone, Region, InstanceType, Pool string
	// "spot", "on-demand", or "" when no label says which.
	CapacityType string
	// From allocatable, what pods can actually claim; CPU and Memory stay on
	// capacity, as the Python app did.
	Extended map[string]int64
	// All labels, for node selectors and affinity.
	Labels map[string]string
	// Capacity minus system reservations: what the scheduler hands out.
	AllocCPU    int64
	AllocMemory int64
	AllocPods   int64
}

// Kubernetes adds this taint itself on cordon; spec.unschedulable already
// reports it, so counting it would mark every cordoned node twice.
const cordonTaint = "node.kubernetes.io/unschedulable"

var pressureConditions = []string{"MemoryPressure", "DiskPressure", "PIDPressure"}

const (
	zoneLabel         = "topology.kubernetes.io/zone"
	regionLabel       = "topology.kubernetes.io/region"
	instanceTypeLabel = "node.kubernetes.io/instance-type"
)

// Node pool labels by provider, most specific first: Karpenter also runs on
// EKS nodes that carry a nodegroup label.
var PoolLabels = []string{
	"karpenter.sh/nodepool",
	"eks.amazonaws.com/nodegroup",
	"cloud.google.com/gke-nodepool",
	"kubernetes.azure.com/agentpool",
}

// Capacity-type labels by provider, most specific first like PoolLabels, and
// the label=value pairs that say spot or on-demand. Other values (Karpenter's
// "reserved", GKE's "standard") say nothing, so they fall through.
var capacityLabels = []string{
	"karpenter.sh/capacity-type",
	"eks.amazonaws.com/capacityType",
	"cloud.google.com/gke-spot",
	"cloud.google.com/gke-provisioning",
	"kubernetes.azure.com/scalesetpriority",
}

var capacityTypes = map[string]string{
	"karpenter.sh/capacity-type=spot":               "spot",
	"karpenter.sh/capacity-type=on-demand":          "on-demand",
	"eks.amazonaws.com/capacityType=SPOT":           "spot",
	"eks.amazonaws.com/capacityType=ON_DEMAND":      "on-demand",
	"cloud.google.com/gke-spot=true":                "spot",
	"cloud.google.com/gke-provisioning=spot":        "spot",
	"kubernetes.azure.com/scalesetpriority=spot":    "spot",
	"kubernetes.azure.com/scalesetpriority=regular": "on-demand",
}

// TopologyLabels are the node labels the dashboard reads.
var TopologyLabels = append(append([]string{zoneLabel, regionLabel, instanceTypeLabel}, PoolLabels...), capacityLabels...)

// extended keeps every non-zero resource besides CPU and memory (GPUs,
// ephemeral-storage, hugepages) in its base unit: bytes or devices. nil when
// there are none. "pods" is a node's pod slot count, not something pods request.
func extended(l corev1.ResourceList) map[string]int64 {
	var out map[string]int64
	for name, q := range l {
		if name == corev1.ResourceCPU || name == corev1.ResourceMemory || name == corev1.ResourcePods || q.IsZero() {
			continue
		}
		if out == nil {
			out = map[string]int64{}
		}
		out[string(name)] = q.Value()
	}
	return out
}

func container(c corev1.Container) Container {
	out := Container{
		Name:     c.Name,
		CPU:      c.Resources.Requests.Cpu().MilliValue(),
		Memory:   c.Resources.Requests.Memory().Value(),
		Extended: extended(c.Resources.Requests),
	}
	if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
		v := q.Value()
		out.MemoryLimit = &v
	}
	return out
}

// bounded reports whether a pod has a ceiling on r: the pod sets one itself,
// or every container that keeps running (regular ones and sidecars) does.
// Plain init containers finish first, so they never bound the running pod.
func bounded(p *corev1.Pod, r corev1.ResourceName) bool {
	if p.Spec.Resources != nil {
		if _, ok := p.Spec.Resources.Limits[r]; ok {
			return true
		}
	}
	for _, c := range p.Spec.Containers {
		if _, ok := c.Resources.Limits[r]; !ok {
			return false
		}
	}
	for _, c := range p.Spec.InitContainers {
		sidecar := c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
		if _, ok := c.Resources.Limits[r]; sidecar && !ok {
			return false
		}
	}
	return len(p.Spec.Containers) > 0
}

func containers(cs []corev1.Container) []Container {
	out := make([]Container, 0, len(cs))
	for _, c := range cs {
		out = append(out, container(c))
	}
	return out
}

func FromPod(p *corev1.Pod) Pod {
	// The scheduler's own formula: max(sum regular, max init), sidecars,
	// pod overhead and pod-level resources included, and max(spec, allocated)
	// while an in-place resize is in flight.
	req := resourcehelper.PodRequests(p, resourcehelper.PodResourcesOptions{
		UseStatusResources: true,
		InPlacePodLevelResourcesVerticalScalingEnabled: true,
	})
	out := Pod{
		Name:         p.Name,
		NodeName:     p.Spec.NodeName,
		Namespace:    p.Namespace,
		CPU:          req.Cpu().MilliValue(),
		Memory:       req.Memory().Value(),
		Containers:   containers(p.Spec.Containers),
		Labels:       p.Labels,
		QOS:          string(p.Status.QOSClass),
		Extended:     extended(req),
		NodeSelector: p.Spec.NodeSelector,
		Affinity:     p.Spec.Affinity,
		Tolerations:  p.Spec.Tolerations,
		Phase:        string(p.Status.Phase),
		Statuses:     statuses(p),
	}
	if ref := metav1.GetControllerOf(p); ref != nil {
		out.Controller = ref.Kind
	}
	// Native sidecars run for the pod's whole life, so they count as regular.
	for _, c := range p.Spec.InitContainers {
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			out.Containers = append(out.Containers, container(c))
		} else {
			out.InitContainers = append(out.InitContainers, container(c))
		}
	}
	if r := p.Spec.Resources; r != nil {
		out.PodLevel = container(corev1.Container{Resources: *r})
	}
	lim := resourcehelper.PodLimits(p, resourcehelper.PodResourcesOptions{})
	if bounded(p, corev1.ResourceCPU) {
		v := lim.Cpu().MilliValue()
		out.CPULimit = &v
	}
	if bounded(p, corev1.ResourceMemory) {
		v := lim.Memory().Value()
		out.MemoryLimit = &v
	}
	return out
}

// statuses lists init containers first, like the pod spec. A container that
// has stopped reports its own exit; a running one reports the previous run's.
func statuses(p *corev1.Pod) []ContainerStatus {
	var out []ContainerStatus
	for _, c := range slices.Concat(p.Status.InitContainerStatuses, p.Status.ContainerStatuses) {
		s := ContainerStatus{Name: c.Name, Ready: c.Ready, Restarts: c.RestartCount}
		if w := c.State.Waiting; w != nil {
			s.Waiting = w.Reason
		}
		last := c.LastTerminationState.Terminated
		if t := c.State.Terminated; t != nil {
			last = t
		}
		if last != nil {
			s.LastExitReason, s.LastExitCode = last.Reason, last.ExitCode
		}
		out = append(out, s)
	}
	return out
}

func FromNode(n *corev1.Node) Node {
	var taints []Taint
	for _, t := range n.Spec.Taints {
		if t.Key != cordonTaint {
			taints = append(taints, Taint{Key: t.Key, Value: t.Value, Effect: string(t.Effect)})
		}
	}
	status := map[corev1.NodeConditionType]corev1.ConditionStatus{}
	for _, c := range n.Status.Conditions {
		status[c.Type] = c.Status
	}
	conditions := map[string]bool{}
	for _, name := range pressureConditions {
		conditions[name] = status[corev1.NodeConditionType(name)] == corev1.ConditionTrue
	}
	// Unknown means the API server lost the kubelet: not ready. No Ready
	// condition at all means nothing was reported, so don't invent an outage.
	ready, ok := status[corev1.NodeReady]
	conditions["Ready"] = !ok || ready == corev1.ConditionTrue
	pool := ""
	for _, l := range PoolLabels {
		if v := n.Labels[l]; v != "" {
			pool = v
			break
		}
	}
	capacity := ""
	for _, l := range capacityLabels {
		if v, ok := capacityTypes[l+"="+n.Labels[l]]; ok {
			capacity = v
			break
		}
	}
	return Node{
		Name:          n.Name,
		CPU:           n.Status.Capacity.Cpu().MilliValue(),
		Memory:        n.Status.Capacity.Memory().Value(),
		Unschedulable: n.Spec.Unschedulable,
		Taints:        taints,
		Conditions:    conditions,
		Zone:          n.Labels[zoneLabel],
		Region:        n.Labels[regionLabel],
		InstanceType:  n.Labels[instanceTypeLabel],
		Pool:          pool,
		CapacityType:  capacity,
		Extended:      extended(n.Status.Allocatable),
		Labels:        n.Labels,
		AllocCPU:      n.Status.Allocatable.Cpu().MilliValue(),
		AllocMemory:   n.Status.Allocatable.Memory().Value(),
		AllocPods:     n.Status.Allocatable.Pods().Value(),
	}
}
