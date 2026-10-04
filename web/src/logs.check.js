// Check for logs.jsx and the logs highlight. Paste into the devtools console
// of a running dashboard (make run ARGS=--synthetic=12x14); it returns "ok".
(() => {
  const L = window.k8sLogs;
  const text = el => (Array.isArray(el) ? el.map(text).join("") : typeof el === "string" ? el : `[${el.props.children}]`);
  const got = {
    plain: text(L.markLine("no filter here", "")),
    marked: text(L.markLine("a out of memory b out of memory", "out of memory")),
    url: L.logsURL("kind", { namespace: "ns", name: "p", container: "app", previous: true, tail: 200 }),
    names: L.containerNames({ containers: [{ name: "app" }, { name: "(pod-level)" }, { name: "migrate (init)", init: true }] }),
    sel: L.selFor({ namespace: "ns", name: "p", containers: [{ name: "init (init)", init: true }, { name: "app" }] }),
  };
  const want = {
    plain: "no filter here",
    marked: "a [out of memory] b [out of memory]",
    url: "/api/logs?context=kind&namespace=ns&pod=p&container=app&tail=200&previous=1",
    names: ["app", "migrate"],
    sel: { namespace: "ns", name: "p", container: "app", previous: false, tail: 200 },
  };
  if (JSON.stringify(got) !== JSON.stringify(want)) return { FAIL: got };

  const pod = { namespace: "ns", name: "p" };
  const nodes = [{ name: "n1", pods: [{ namespace: "ns", name: "q" }] }, { name: "n2", pods: [pod] }];
  const none = { active: false, pods: new Set(), dimNodes: new Set(), count: 0, errors: [] };
  const lit = window.k8sHighlight.pickShown({ match: none, tab: "logs", nodes, logsPod: { namespace: "ns", name: "p" } });
  if (lit.lit !== "logs" || lit.ring !== "n2" || !lit.pods.has(pod)) return { FAIL: lit };
  const typed = window.k8sHighlight.pickShown({ match: { ...none, active: true }, tab: "logs", nodes, logsPod: pod });
  if (typed.lit !== null) return { FAIL: "a typed query must win" };
  const crashed = L.selFor({ namespace: "ns", name: "p", containers: [{ name: "app" }], statuses: [{ name: "app", restarts: 3 }] });
  if (!crashed.previous) return { FAIL: "a restarted container should open on its previous run" };
  return "ok";
})()
