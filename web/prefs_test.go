package web

import "testing"

func TestResolveTheme(t *testing.T) {
	vm := jsVM(t, "prefs.jsx")
	for expr, want := range map[string]string{
		`k8sPrefs.resolveTheme("system", true)`:  `"dark"`,
		`k8sPrefs.resolveTheme("system", false)`: `"light"`,
		`k8sPrefs.resolveTheme("light", true)`:   `"light"`,
		`k8sPrefs.resolveTheme("dark", false)`:   `"dark"`,
		`k8sPrefs.resolveTheme("bogus", true)`:   `"dark"`,
	} {
		if got := js(t, vm, expr); got != want {
			t.Errorf("%s = %s, want %s", expr, got, want)
		}
	}
}

func TestPrefsSurviveBrokenStorage(t *testing.T) {
	vm := jsVM(t, "prefs.jsx")
	if _, err := vm.RunString(`
		var isTheme = v => k8sPrefs.THEME_PREFS.includes(v);
		var mem = { m: {}, getItem(k) { return k in this.m ? this.m[k] : null }, setItem(k, v) { this.m[k] = v } };
		var denied = { getItem() { throw new Error("denied") }, setItem() { throw new Error("denied") } };`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ expr, want string }{
		{`k8sPrefs.readPref(null, "k8sfoams.theme", "system", isTheme)`, `"system"`},
		{`k8sPrefs.readPref(denied, "k8sfoams.theme", "system", isTheme)`, `"system"`},
		{`(mem.setItem("k8sfoams.theme", "{not json"), k8sPrefs.readPref(mem, "k8sfoams.theme", "system", isTheme))`, `"system"`},
		{`(mem.setItem("k8sfoams.theme", '"sepia"'), k8sPrefs.readPref(mem, "k8sfoams.theme", "system", isTheme))`, `"system"`},
		{`(k8sPrefs.writePref(mem, "k8sfoams.theme", "dark"), k8sPrefs.readPref(mem, "k8sfoams.theme", "system", isTheme))`, `"dark"`},
		{`(k8sPrefs.writePref(denied, "k8sfoams.theme", "dark"), "no throw")`, `"no throw"`},
		{`(k8sPrefs.writePref(null, "k8sfoams.theme", "dark"), "no throw")`, `"no throw"`},
	} {
		if got := js(t, vm, c.expr); got != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}
}
