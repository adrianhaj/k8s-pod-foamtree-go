package web

import (
	"testing"

	"github.com/dop251/goja"
)

func paletteVM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := jsVM(t, "nodestatus.jsx", "podaudit.jsx", "qos.jsx", "palette.jsx")
	if _, err := vm.RunString(`
		var mk = (ns, cpu) => ({ namespace: ns, cpu: cpu, name: ns + "-" + cpu, findings: [], qos: "Burstable" });
		var nodes = [
			{ name: "a", pods: [mk("big", 5000), mk("mid", 3000), mk("small", 100)] },
			{ name: "b", pods: [mk("big", 1000), mk("n4", 900), mk("n5", 800), mk("n6", 700), mk("tiny", 50)] },
		];
		var entries = m => [...m];`); err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestAssignNamespaces(t *testing.T) {
	vm := paletteVM(t)
	for _, c := range []struct{ name, expr, want string }{
		{"top five by CPU", `entries(k8sPalette.assignNamespaces(nodes, null))`,
			`[["big",0],["mid",1],["n4",2],["n5",3],["n6",4]]`},
		{"slots stick and a freed slot is reused",
			`entries(k8sPalette.assignNamespaces(nodes, new Map([["n6", 0], ["gone", 1]])))`,
			`[["n6",0],["big",1],["mid",2],["n4",3],["n5",4]]`},
		{"ties break alphabetically",
			`entries(k8sPalette.assignNamespaces([{ name: "x", pods: [mk("b", 10), mk("a", 10)] }], null))`,
			`[["a",0],["b",1]]`},
		{"empty cluster", `entries(k8sPalette.assignNamespaces([], null))`, `[]`},
	} {
		if got := js(t, vm, c.expr); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestPodToken(t *testing.T) {
	vm := paletteVM(t)
	for expr, want := range map[string]string{
		`k8sPalette.podToken({ qos: "BestEffort" }, "qos", new Map())`:                             `"--danger"`,
		`k8sPalette.podToken({ qos: "Guaranteed" }, "qos", new Map())`:                             `"--ok"`,
		`k8sPalette.podToken({ qos: "Weird" }, "qos", new Map())`:                                  `"--pod-neutral"`,
		`k8sPalette.podToken({ findings: ["missing-limits"] }, "problems", new Map())`:             `"--info"`,
		`k8sPalette.podToken({ findings: ["missing-limits", "monolith"] }, "problems", new Map())`: `"--warn"`,
		`k8sPalette.podToken({ findings: [] }, "problems", new Map())`:                             `"--pod-neutral"`,
		`k8sPalette.podToken({ namespace: "big" }, "namespace", new Map([["big", 2]]))`:            `"--ns-2"`,
		`k8sPalette.podToken({ namespace: "rare" }, "namespace", new Map([["big", 2]]))`:           `"--ns-other"`,
	} {
		if got := js(t, vm, expr); got != want {
			t.Errorf("%s = %s, want %s", expr, got, want)
		}
	}
}

func TestUtilToneAndMixing(t *testing.T) {
	vm := paletteVM(t)
	for expr, want := range map[string]string{
		`[0.85, 0.851, 0.95, 0.951, 0, NaN].map(k8sPalette.utilTone)`: `[null,"warn","warn","danger",null,null]`,
		`k8sPalette.tintHex("#2a78d6", "#ffffff", 0.16)`:              `"#dde9f8"`,
		`k8sPalette.rgbHex(k8sPalette.shade([1, 0.5, 0], 0.5))`:       `"#804000"`,
		`k8sPalette.rgbHex(k8sPalette.hexRgb("#1a1e25"))`:             `"#1a1e25"`,
	} {
		if got := js(t, vm, expr); got != want {
			t.Errorf("%s = %s, want %s", expr, got, want)
		}
	}
}
