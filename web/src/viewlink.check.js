// Check for the view-link helpers in prefs.jsx. Go tests never run JS: paste
// this into the devtools console of a running dashboard; it returns "ok".
(() => {
  const { readViewParams, viewSearch } = window.k8sPrefs;
  const allowed = { view: ["2d", "3d"], group: ["none", "zone"], color: ["namespace", "qos"] };
  const cases = [
    ["", {}],
    ["?view=3d&group=zone&size=nvidia.com%2Fgpu", { view: "3d", group: "zone", size: "nvidia.com/gpu" }],
    ["?view=4d&group=planet&color=rainbow", {}],
    ["?q=app%3D%22my+app%22+ns%3Ax&context=kind-a", { q: 'app="my app" ns:x', context: "kind-a" }],
    ["?unknown=1&color=qos", { color: "qos" }],
  ];
  for (const [search, diffs] of cases) {
    const want = { context: "", view: "2d", size: "cpu", group: "none", color: "namespace", q: "", ...diffs };
    const got = readViewParams(search, allowed);
    if (JSON.stringify(got) !== JSON.stringify(want)) return { FAIL: search, got };
    const back = readViewParams(viewSearch(got), allowed);
    if (JSON.stringify(back) !== JSON.stringify(want)) return { FAIL: "round trip " + search, back };
  }
  if (viewSearch(readViewParams("", allowed)) !== "") return { FAIL: "defaults must give a clean URL" };
  if (viewSearch({ ...readViewParams("", allowed), view: "3d" }) !== "?view=3d") return { FAIL: "only non-defaults are written" };
  return "ok";
})()
