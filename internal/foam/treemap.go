package foam

import (
	"cmp"
	"slices"
)

type Axis int

const (
	CPU Axis = iota
	Memory
)

func (a Axis) container(c Container) int64 {
	if a == CPU {
		return c.CPU
	}
	return c.Memory
}

func (a Axis) pod(p Pod) int64 {
	if a == CPU {
		return p.CPU
	}
	return p.Memory
}

// limit is nil for a pod with no ceiling on this axis.
func (a Axis) limit(p Pod) *float64 {
	l := p.CPULimit
	if a == Memory {
		l = p.MemoryLimit
	}
	if l == nil {
		return nil
	}
	v := a.weight(*l)
	return &v
}

func (a Axis) node(n Node) int64 {
	if a == CPU {
		return n.CPU
	}
	return n.Memory
}

// weight converts to the frontend's units: millicores, or decimal kB. Sums
// stay int64 until here so an exactly full node has an exactly empty leaf.
func (a Axis) weight(v int64) float64 {
	if a == CPU {
		return float64(v)
	}
	return float64(v) / 1000
}

type Tree struct {
	Groups []NodeGroup `json:"groups"`
	// Pods no node has taken yet: nothing to draw, but the panel lists them.
	Pending []PendingPod `json:"pending"`
}

type PendingPod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
}

type NodeGroup struct {
	Label         string           `json:"label"`
	Weight        float64          `json:"weight"`
	Groups        []any            `json:"groups"` // PodGroup..., then the "empty" Leaf
	Unschedulable bool             `json:"unschedulable"`
	Taints        []Taint          `json:"taints"`
	Conditions    map[string]bool  `json:"conditions"`
	Warnings      []string         `json:"warnings"`
	Zone          string           `json:"zone"`
	Region        string           `json:"region"`
	InstanceType  string           `json:"instanceType"`
	Pool          string           `json:"pool"`
	CapacityType  string           `json:"capacityType"`
	Extended      map[string]int64 `json:"extended,omitempty"`
}

type PodGroup struct {
	Label             string            `json:"label"`
	Weight            float64           `json:"weight"`
	Groups            []Leaf            `json:"groups"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels"`
	QOS               string            `json:"qos"`
	HasInitContainers bool              `json:"hasInitContainers"`
	Findings          []string          `json:"findings"`
	Phase             string            `json:"phase"`
	Statuses          []ContainerStatus `json:"statuses"`
	Limit             *float64          `json:"limit"`
	Extended          map[string]int64  `json:"extended,omitempty"`
	Resize            *PodResize        `json:"resize,omitempty"`
}

type PodResize struct {
	State   string  `json:"state"`
	Message string  `json:"message"`
	Desired float64 `json:"desired"`
}

type Leaf struct {
	Label    string           `json:"label"`
	Weight   float64          `json:"weight"`
	Color    string           `json:"color,omitempty"`
	Extended map[string]int64 `json:"extended,omitempty"`
}

const (
	emptyColor = "#ffffff"
	// The frontend tells init containers apart by the presence of a color.
	initColor = "#aaaaaa"
)

// Treemap nests node → pod → container on one axis and adds an "empty" leaf
// per node for free capacity. Output is sorted by name: the informer cache
// has no stable order, and a reshuffled layout on every refresh is unusable.
func Treemap(nodes []Node, pods []Pod, axis Axis, a Audit) Tree {
	byNode := map[string][]Pod{}
	for _, p := range pods {
		byNode[p.NodeName] = append(byNode[p.NodeName], p)
	}
	nodes = slices.SortedFunc(slices.Values(nodes), func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })

	tree := Tree{Groups: make([]NodeGroup, 0, len(nodes))}
	for _, n := range nodes {
		onNode := byNode[n.Name]
		slices.SortFunc(onNode, func(a, b Pod) int {
			return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
		})
		groups := make([]any, 0, len(onNode)+1)
		var used int64
		for _, p := range onNode {
			groups = append(groups, podGroup(p, n, axis, a))
			used += axis.pod(p)
		}
		groups = append(groups, Leaf{Label: "empty", Weight: axis.weight(axis.node(n) - used), Color: emptyColor})
		tree.Groups = append(tree.Groups, NodeGroup{
			Label:         n.Name,
			Weight:        axis.weight(axis.node(n)),
			Groups:        groups,
			Unschedulable: n.Unschedulable,
			Taints:        orEmpty(n.Taints),
			Conditions:    orEmptyMap(n.Conditions),
			Warnings:      Warnings(n),
			Zone:          n.Zone,
			Region:        n.Region,
			InstanceType:  n.InstanceType,
			Pool:          n.Pool,
			CapacityType:  n.CapacityType,
			Extended:      n.Extended,
		})
	}
	tree.Pending = make([]PendingPod, 0, len(byNode[""]))
	for _, p := range sortPods(byNode[""]) {
		tree.Pending = append(tree.Pending, PendingPod{Namespace: p.Namespace, Name: p.Name, Reason: p.SchedReason, Message: p.SchedMessage})
	}
	return tree
}

func podGroup(p Pod, n Node, axis Axis, a Audit) PodGroup {
	leaves := make([]Leaf, 0, len(p.Containers)+len(p.InitContainers)+1)
	rest := axis.container(p.PodLevel)
	for _, c := range p.Containers {
		leaves = append(leaves, Leaf{Label: c.Name, Weight: axis.weight(axis.container(c)), Extended: c.Extended})
		rest -= axis.container(c)
	}
	// Pod-level budget no container claims would otherwise draw as nothing.
	if rest > 0 {
		leaves = append(leaves, Leaf{Label: "(pod-level)", Weight: axis.weight(rest)})
	}
	for _, c := range p.InitContainers {
		if w := axis.container(c); w > 0 {
			leaves = append(leaves, Leaf{Label: c.Name + " (init)", Weight: axis.weight(w), Color: initColor, Extended: c.Extended})
		}
	}
	var rz *PodResize
	if r := p.Resize; r != nil {
		rz = &PodResize{State: r.State, Message: r.Message, Desired: axis.weight(axis.container(r.Desired))}
	}
	return PodGroup{
		Label:             p.Name,
		Weight:            axis.weight(axis.pod(p)),
		Groups:            leaves,
		Namespace:         p.Namespace,
		Labels:            orEmptyMap(p.Labels),
		QOS:               p.QOS,
		HasInitContainers: len(p.InitContainers) > 0,
		Findings:          Findings(p, n, a),
		Phase:             p.Phase,
		Statuses:          orEmpty(p.Statuses),
		Limit:             axis.limit(p),
		Extended:          p.Extended,
		Resize:            rz,
	}
}

// JSON null would break the frontend's `.length` / key lookups.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func orEmptyMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}
