// Package foam turns Kubernetes nodes and pods into the treemap JSON the
// dashboard renders. It is pure: no client-go calls, only API types in.
package foam

import (
	corev1 "k8s.io/api/core/v1"
	resourcehelper "k8s.io/component-helpers/resource"
)

// CPU is in millicores, memory in bytes — the scheduler's own units.
type Container struct {
	Name   string
	CPU    int64
	Memory int64
	// nil when unset: an unbounded container is exactly what the audit flags.
	MemoryLimit *int64
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
}

// Kubernetes adds this taint itself on cordon; spec.unschedulable already
// reports it, so counting it would mark every cordoned node twice.
const cordonTaint = "node.kubernetes.io/unschedulable"

var pressureConditions = []string{"MemoryPressure", "DiskPressure", "PIDPressure"}

func container(c corev1.Container) Container {
	out := Container{
		Name:   c.Name,
		CPU:    c.Resources.Requests.Cpu().MilliValue(),
		Memory: c.Resources.Requests.Memory().Value(),
	}
	if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
		v := q.Value()
		out.MemoryLimit = &v
	}
	return out
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
		Name:       p.Name,
		NodeName:   p.Spec.NodeName,
		Namespace:  p.Namespace,
		CPU:        req.Cpu().MilliValue(),
		Memory:     req.Memory().Value(),
		Containers: containers(p.Spec.Containers),
		Labels:     p.Labels,
		QOS:        string(p.Status.QOSClass),
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
	return Node{
		Name:          n.Name,
		CPU:           n.Status.Capacity.Cpu().MilliValue(),
		Memory:        n.Status.Capacity.Memory().Value(),
		Unschedulable: n.Spec.Unschedulable,
		Taints:        taints,
		Conditions:    conditions,
	}
}
