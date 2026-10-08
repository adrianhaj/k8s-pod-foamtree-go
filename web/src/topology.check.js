// Check for the stranded-capacity and idle-cost helpers in topology.jsx. Go
// tests never run JS: paste this into the devtools console of a running
// dashboard (any --synthetic size); it logs "ok" or throws on the first mismatch.
(() => {
  const T = window.k8sTopology;
  const pod = (cpu, mem) => ({ cpu, mem });
  const node = (name, cpuFree, memFree, pods = [], warnings = []) => ({ name, cpuFree, memFree, pods, warnings });
  const eq = (got, want, what) => {
    if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(`${what}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  };

  eq(T.podShape([]), null, "shape: empty cluster");
  eq(T.podShape([node("a", 1000, 1024)]), null, "shape: no pods");
  eq(T.podShape([node("a", 0, 0, [pod(0, 512), pod(100, 0)])]), null, "shape: no pod requests both");
  eq(T.podShape([node("a", 0, 0, [pod(100, 100), pod(100, 400)]), node("b", 0, 0, [pod(100, 200)])]), 2, "shape: odd median across nodes");
  eq(T.podShape([node("a", 0, 0, [pod(100, 100), pod(100, 300)])]), 2, "shape: even median");

  // shape 2 = 2 MiB per millicore
  eq(T.stranded(node("a", 1000, 2000), 2), { cpu: 0, mem: 0 }, "stranded: balanced");
  eq(T.stranded(node("a", 1000, 0), 2), { cpu: 1000, mem: 0 }, "stranded: zero free memory strands all cpu");
  eq(T.stranded(node("a", 0, 4096), 2), { cpu: 0, mem: 4096 }, "stranded: zero free cpu strands all memory");
  eq(T.stranded(node("a", 1000, 1000), 2), { cpu: 500, mem: 0 }, "stranded: memory runs out first");
  eq(T.stranded(node("a", 1000, 3000), 2), { cpu: 0, mem: 1000 }, "stranded: cpu runs out first");
  eq(T.stranded(node("a", 0, 0), 2), { cpu: 0, mem: 0 }, "stranded: full node");
  eq(T.stranded(node("a", 1000, 0), null), { cpu: 0, mem: 0 }, "stranded: no shape");

  eq(T.largestFit([], 2), null, "fit: empty cluster");
  eq(T.largestFit([node("a", 1000, 2000)], null), null, "fit: no shape");
  eq(T.largestFit([node("a", 0, 0), node("b", 0, 0)], 2), { node: "a", cpu: 0, mem: 0 }, "fit: all nodes full");
  eq(T.largestFit([node("a", 1000, 1000), node("b", 800, 4000), node("c", 4000, 8000, [], ["cordoned"])], 2),
    { node: "b", cpu: 800, mem: 1600 }, "fit: best warning-free node");
  eq(T.largestFit([node("c", 4000, 8000, [], ["cordoned"])], 2), null, "fit: no warning-free node");

  const priced = (cost, pods) => ({ cost, pods: pods.map(c => ({ cost: c })) });
  eq(T.idleCost([]), null, "idle: empty cluster");
  eq(T.idleCost([priced(null, [1])]), null, "idle: nothing priced");
  eq(T.idleCost([priced(1, [0.25, 0.25]), priced(2, []), priced(null, [])]), { idle: 2.5, total: 3, unpriced: 1 }, "idle: price minus pod costs");
  eq(T.idleCost([priced(1, [0.75, 0.5])]), { idle: 0, total: 1, unpriced: 0 }, "idle: overpaid node is 0");

  console.log("ok");
})();
