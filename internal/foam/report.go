package foam

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// ReportRow is one pod with its node and both axes side by side, the flat
// shape a spreadsheet wants. A node without pods gets one row with the pod
// columns empty, so its free capacity still shows.
type ReportRow struct {
	Node             string   `json:"node"`
	Zone             string   `json:"zone"`
	Pool             string   `json:"pool"`
	InstanceType     string   `json:"instanceType"`
	NodeCPU          int64    `json:"nodeCpu"`
	NodeMemoryBytes  int64    `json:"nodeMemoryBytes"`
	NodeWarnings     []string `json:"nodeWarnings"`
	Namespace        string   `json:"namespace"`
	Pod              string   `json:"pod"`
	QOS              string   `json:"qos"`
	CPU              int64    `json:"cpu"`
	CPULimit         *int64   `json:"cpuLimit"`
	MemoryBytes      int64    `json:"memoryBytes"`
	MemoryLimitBytes *int64   `json:"memoryLimitBytes"`
	Findings         []string `json:"findings"`
	// Estimated USD per hour; null when the node has no price.
	NodeHourlyCost *float64 `json:"nodeHourlyCost"`
	HourlyCost     *float64 `json:"hourlyCost"`
}

// Report lists nodes by name with their pods by namespace then name, like the
// treemap, then the pods the treemap cannot draw: pending ones and any whose
// node is gone. Those keep their nodeName (empty while pending) and have no
// node columns or warnings, only the findings that need no node (requests,
// limits and the crash signals).
func Report(nodes []Node, pods []Pod, a Audit) []ReportRow {
	byNode := map[string][]Pod{}
	for _, p := range pods {
		byNode[p.NodeName] = append(byNode[p.NodeName], p)
	}
	nodes = slices.SortedFunc(slices.Values(nodes), func(a, b Node) int { return cmp.Compare(a.Name, b.Name) })
	rows := []ReportRow{}
	for _, n := range nodes {
		base := ReportRow{
			Node: n.Name, Zone: n.Zone, Pool: n.Pool, InstanceType: n.InstanceType,
			NodeCPU: n.CPU, NodeMemoryBytes: n.Memory, NodeWarnings: Warnings(n), Findings: []string{},
			NodeHourlyCost: n.HourlyPrice,
		}
		if len(byNode[n.Name]) == 0 {
			rows = append(rows, base)
		}
		rows = appendPods(rows, base, n, byNode[n.Name], a)
		delete(byNode, n.Name)
	}
	var unplaced []Pod
	for _, ps := range byNode {
		unplaced = append(unplaced, ps...)
	}
	for _, p := range sortPods(unplaced) {
		rows = appendPods(rows, ReportRow{Node: p.NodeName, NodeWarnings: []string{}}, Node{}, []Pod{p}, a)
	}
	return rows
}

func sortPods(ps []Pod) []Pod {
	slices.SortFunc(ps, func(a, b Pod) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return ps
}

// n is the zero Node for a pod on no known node: Findings then reports only
// what needs no node (missing requests / limits and the crash signals).
func appendPods(rows []ReportRow, base ReportRow, n Node, pods []Pod, a Audit) []ReportRow {
	for _, p := range sortPods(pods) {
		r := base
		r.Namespace, r.Pod, r.QOS = p.Namespace, p.Name, p.QOS
		r.CPU, r.CPULimit, r.MemoryBytes, r.MemoryLimitBytes = p.CPU, p.CPULimit, p.Memory, p.MemoryLimit
		r.Findings = Findings(p, n, a)
		r.HourlyCost = PodCost(p, n)
		rows = append(rows, r)
	}
	return rows
}

// ShowbackRow totals the pods of one namespace or label value.
type ShowbackRow struct {
	Group        string `json:"group"`
	Pods         int    `json:"pods"`
	UnpricedPods int    `json:"unpricedPods"`
	CPU          int64  `json:"cpu"`
	MemoryBytes  int64  `json:"memoryBytes"`
	// Estimated USD per hour of the priced pods; null when none is priced.
	HourlyCost *float64 `json:"hourlyCost"`
	cost       float64
}

// Showback totals the pods on known nodes by namespace, or by the value of
// the label after "label:", where pods without it share the "" group.
// Pending pods reserve nothing and are left out. Costliest first, then by
// group.
func Showback(nodes []Node, pods []Pod, groupBy string) ([]ShowbackRow, error) {
	key, byLabel := strings.CutPrefix(groupBy, "label:")
	if groupBy != "namespace" && (!byLabel || key == "") {
		return nil, fmt.Errorf("groupBy must be namespace or label:<key>, got %q", groupBy)
	}
	byName := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}
	groups := map[string]*ShowbackRow{}
	for _, p := range pods {
		n, ok := byName[p.NodeName]
		if !ok {
			continue
		}
		g := p.Namespace
		if byLabel {
			g = p.Labels[key]
		}
		r := groups[g]
		if r == nil {
			r = &ShowbackRow{Group: g}
			groups[g] = r
		}
		r.Pods++
		r.CPU += p.CPU
		r.MemoryBytes += p.Memory
		if c := PodCost(p, n); c == nil {
			r.UnpricedPods++
		} else {
			r.cost += *c
		}
	}
	rows := make([]ShowbackRow, 0, len(groups))
	for _, r := range groups {
		if r.Pods > r.UnpricedPods {
			r.HourlyCost = &r.cost
		}
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(a, b ShowbackRow) int {
		return cmp.Or(cmp.Compare(b.cost, a.cost), cmp.Compare(a.Group, b.Group))
	})
	return rows, nil
}
