package web

import "testing"

func TestProblems(t *testing.T) {
	vm := jsVM(t, "nodestatus.jsx", "podaudit.jsx", "problems.jsx")
	if _, err := vm.RunString(`
		var nodes = [
			{ name: "n1", warnings: ["cordoned", "tainted"],
			  taints: [{ key: "dedicated", value: "batch", effect: "NoSchedule" }, { key: "soft", value: "", effect: "PreferNoSchedule" }],
			  pods: [{ namespace: "pay", name: "api-1", findings: ["missing-limits"] }] },
			{ name: "n2", warnings: ["memory-pressure"], taints: [],
			  pods: [{ namespace: "pay", name: "db-0", findings: ["monolith", "missing-limits"] }, { namespace: "ops", name: "ok-1", findings: [] }] },
		];
		var rows = k8sProblems.buildProblems(nodes);`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, expr, want string }{
		{"order", `rows.map(r => r.rule)`, `["cordoned","mem pressure","monolith","tainted","no memory limit","no memory limit"]`},
		{"objects", `rows.map(r => r.object)`, `["node/n1","node/n2","pay/db-0","node/n1","pay/api-1","pay/db-0"]`},
		{"node details", `rows.filter(r => r.kind === "node").map(r => r.detail)`,
			`["spec.unschedulable=true","MemoryPressure=True","dedicated=batch:NoSchedule"]`},
		{"queries", `rows.map(r => r.query)`,
			`["health:cordoned","health:memory-pressure","audit:monolith","health:tainted","audit:missing-limits","audit:missing-limits"]`},
		{"chips", `k8sProblems.problemChips(rows).map(c => c.query + "=" + c.count)`,
			`["health:cordoned=1","health:memory-pressure=1","audit:monolith=1","audit:missing-limits=2","health:tainted=1"]`},
		{"healthy cluster", `k8sProblems.buildProblems([{ name: "x", warnings: [], pods: [] }])`, `[]`},
		{"no chips", `k8sProblems.problemChips([])`, `[]`},
	} {
		if got := js(t, vm, c.expr); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
