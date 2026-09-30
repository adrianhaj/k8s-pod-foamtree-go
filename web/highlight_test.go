package web

import "testing"

func TestPickShown(t *testing.T) {
	vm := jsVM(t, "highlight.jsx")
	if _, err := vm.RunString(`
		var p = n => ({ name: n });
		var a1 = p("a1"), a2 = p("a2"), b1 = p("b1");
		var nodes = [{ name: "a", pods: [a1, a2] }, { name: "b", pods: [b1] }];
		var none = { active: false, pods: new Set(), dimNodes: new Set(), count: 3, total: 3, errors: [] };
		var typed = { active: true, pods: new Set([b1]), dimNodes: new Set(), count: 1, total: 3, errors: [] };
		var changes = { pods: new Set([a2]) };
		var fit = { active: true, pods: new Set([b1]), dimNodes: new Set(["a"]), count: 1, total: 3, errors: [] };
		var show = args => {
			var s = k8sHighlight.pickShown(Object.assign({ match: none, tab: null, changes: null, sim: null, nodes: nodes }, args));
			return [s.active, [...s.pods].map(x => x.name).sort(), [...s.dimNodes], s.ring, s.lit];
		};`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, expr, want string }{
		{"typed query beats the changes tab", `show({ match: typed, tab: "changes", changes: changes })`, `[true,["b1"],[],null,null]`},
		{"changes tab lights changes", `show({ tab: "changes", changes: changes })`, `[true,["a2"],[],null,"changes"]`},
		{"collapsed panel lights nothing", `show({ tab: null, changes: changes })`, `[false,[],[],null,null]`},
		{"no changes, nothing lit", `show({ tab: "changes", changes: { pods: new Set() } })`, `[false,[],[],null,null]`},
		{"drain lights and rings the node", `show({ tab: "drain", sim: { mode: "drain", node: "a", fit: null } })`, `[true,["a1","a2"],[],"a","drain"]`},
		{"drain node gone after refresh", `show({ tab: "drain", sim: { mode: "drain", node: "gone", fit: null } })`, `[false,[],[],null,null]`},
		{"fit result dims non-fitting nodes", `show({ tab: "drain", sim: { mode: "fit", node: "a", fit: fit } })`, `[true,["b1"],["a"],null,"fit"]`},
		{"fit mode without a result", `show({ tab: "drain", sim: { mode: "fit", node: null, fit: null } })`, `[false,[],[],null,null]`},
		{"problems tab uses the query", `show({ match: typed, tab: "problems" })`, `[true,["b1"],[],null,null]`},
	} {
		if got := js(t, vm, c.expr); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
