package foam

import (
	"cmp"
	"slices"
)

// GroupSummary sums the nodes of one pool and zone, in the scheduler's
// units: millicores and bytes. Requested counts pods on those nodes only.
type GroupSummary struct {
	Pool, Zone              string
	CPU, CPURequested       int64
	Memory, MemoryRequested int64
}

// Summary is a cluster in the few numbers an alert reads. Findings holds
// every rule the audit leaves on and Warnings every NodeWarnings slug, zeros
// included, so a count that drops to zero stays visible.
type Summary struct {
	// Sorted by pool, then zone.
	Groups   []GroupSummary
	Findings map[string]int
	Warnings map[string]int
	Pending  int
}

// Summarize counts like the dashboard: node capacity, the requests of pods
// on a known node, and findings for every pod, one on no known node judged
// against the zero Node as Report does.
func Summarize(nodes []Node, pods []Pod, a Audit) Summary {
	s := Summary{Findings: map[string]int{}, Warnings: map[string]int{}}
	for _, r := range AuditRules {
		if !a.Disabled[r] {
			s.Findings[r] = 0
		}
	}
	for _, w := range NodeWarnings {
		s.Warnings[w] = 0
	}
	byName := map[string]Node{}
	groups := map[[2]string]*GroupSummary{}
	groupOf := map[string]*GroupSummary{}
	for _, n := range nodes {
		byName[n.Name] = n
		k := [2]string{n.Pool, n.Zone}
		if groups[k] == nil {
			groups[k] = &GroupSummary{Pool: n.Pool, Zone: n.Zone}
		}
		g := groups[k]
		g.CPU += n.CPU
		g.Memory += n.Memory
		groupOf[n.Name] = g
		for _, w := range Warnings(n) {
			s.Warnings[w]++
		}
	}
	for _, p := range pods {
		if p.NodeName == "" {
			s.Pending++
		}
		if g := groupOf[p.NodeName]; g != nil {
			g.CPURequested += p.CPU
			g.MemoryRequested += p.Memory
		}
		for _, f := range Findings(p, byName[p.NodeName], a) {
			s.Findings[f]++
		}
	}
	s.Groups = make([]GroupSummary, 0, len(groups))
	for _, g := range groups {
		s.Groups = append(s.Groups, *g)
	}
	slices.SortFunc(s.Groups, func(a, b GroupSummary) int {
		return cmp.Or(cmp.Compare(a.Pool, b.Pool), cmp.Compare(a.Zone, b.Zone))
	})
	return s
}
