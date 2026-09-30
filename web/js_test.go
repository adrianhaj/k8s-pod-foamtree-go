package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// jsVM compiles web/src files the way `make web` does (JSX → IIFE, one file at
// a time) and runs them in one goja VM, so pure modules are unit-tested
// without Node. window is the global object, like in a browser.
func jsVM(t *testing.T, files ...string) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	if err := vm.Set("window", vm.GlobalObject()); err != nil {
		t.Fatal(err)
	}
	// Modules destructure React at load time; none may call it there.
	if _, err := vm.RunString(`var React = { createElement() { return null }, Fragment: {},
		useState() {}, useEffect() {}, useMemo() {}, useRef() {}, useLayoutEffect() {}, useCallback() {} };`); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join("src", f))
		if err != nil {
			t.Fatal(err)
		}
		out := api.Transform(string(src), api.TransformOptions{
			Loader: api.LoaderJSX, Format: api.FormatIIFE, Target: api.ES2017, Sourcefile: f,
		})
		if len(out.Errors) > 0 {
			t.Fatalf("%s: %s", f, out.Errors[0].Text)
		}
		if _, err := vm.RunScript(f, string(out.Code)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	return vm
}

// js evaluates expr and returns it as JSON, so assertions compare strings.
func js(t *testing.T, vm *goja.Runtime, expr string) string {
	t.Helper()
	v, err := vm.RunString("JSON.stringify(" + expr + ")")
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v.String()
}

func TestJSHarnessRunsQueryParser(t *testing.T) {
	vm := jsVM(t, "nodestatus.jsx", "query.jsx")
	got := js(t, vm, `k8sQuery.parseQuery("ns:").errors.map(e => e.message)`)
	if want := `["ns: needs a value"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
