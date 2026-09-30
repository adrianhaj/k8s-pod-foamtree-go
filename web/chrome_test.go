package web

import "testing"

func TestChromeHelpers(t *testing.T) {
	vm := jsVM(t, "nodestatus.jsx", "podaudit.jsx", "qos.jsx", "palette.jsx", "topology.jsx", "format.jsx", "icons.jsx", "chrome.jsx")
	for expr, want := range map[string]string{
		`k8sChrome.providerOf("arn:aws:eks:eu-west-1:1:cluster/prod")`: `"eks"`,
		`k8sChrome.providerOf("gke_proj_europe-west1_main")`:           `"gke"`,
		`k8sChrome.providerOf("kind-kind")`:                            `"k8s"`,
		`[0, 60, 300, 42].map(k8sChrome.refreshLabel)`:                 `["Off","1 min","5 min","42 s"]`,
		`k8sChrome.attentionBySev([
			{ name: "a", warnings: ["cordoned", "memory-pressure"] },
			{ name: "b", warnings: ["memory-pressure"] },
			{ name: "c", warnings: ["disk-pressure"] },
			{ name: "d", warnings: ["memory-pressure"] },
			{ name: "e", warnings: [] },
		])`: `[{"sev":"danger","count":1,"query":"health:cordoned"},{"sev":"warn","count":3,"query":"health:memory-pressure"}]`,
		`k8sChrome.attentionBySev([{ name: "x", warnings: [] }])`: `[]`,
	} {
		if got := js(t, vm, expr); got != want {
			t.Errorf("%s = %s, want %s", expr, got, want)
		}
	}
}
