// Check for llm.jsx. Paste into the devtools console of a running dashboard;
// it returns "ok". It saves and restores the connection settings it touches.
(() => {
  const L = window.k8sLLM;
  const a = L.parseSSE('data: {"type":"delta","text":"a"}\n\ndata: {"type":"del');
  if (a.events.length !== 1 || a.events[0].text !== "a" || a.rest !== 'data: {"type":"del') return { FAIL: "parseSSE split" };
  const b = L.parseSSE(a.rest + 'ta","text":"b"}\n\n');
  if (b.events[0].text !== "b" || b.rest !== "") return { FAIL: "parseSSE join" };

  if (L.estimateTokens("12345678") !== 2 || L.estimateTokens("123456789") !== 3) return { FAIL: "estimate" };
  if (L.estimateTokens("ééééé") !== 2 || L.estimateTokens("😀😀😀😀") !== 1) return { FAIL: "estimate counts code points" };
  if (L.estimateMessages([{ role: "user", content: "12345678" }]) !== 4) return { FAIL: "estimateMessages" };
  if (L.estimateMessages([{ role: "user", content: "😀😀😀😀" }]) !== 3) return { FAIL: "estimateMessages code points" };
  if (L.MIN_ANSWER_TOKENS !== 256) return { FAIL: "MIN_ANSWER_TOKENS" };
  if (!L.overCap(19745, 20000) || L.overCap(19744, 20000) || L.overCap(99999, 0)) return { FAIL: "overCap" };
  if (L.effectiveBudget({ mode: "server", budget: 90000 }, { maxTokens: 50000 }) !== 50000) return { FAIL: "operator cap" };
  if (L.effectiveBudget({ mode: "own", budget: 90000 }, { maxTokens: 50000 }) !== 90000) return { FAIL: "own key ignores the operator cap" };

  const totals = { nodes: 2, pods: 3, cpuUsed: 1500, cpuCap: 4000, memUsed: 1024, memCap: 4096 };
  const problems = [{ sev: "warn", rule: "monolith", object: "ns/p", node: "n1", detail: "big" }];
  const items = L.contextItems({ context: "kind", totals, problems, pod: null, logs: null, node: null });
  if (items.map(i => i.id).join() !== "summary,problems") return { FAIL: items };
  const sys = L.systemPrompt(items, { problems: false });
  if (!sys.includes("2 nodes") || sys.includes("monolith")) return { FAIL: "an unticked item was sent" };
  if (!L.isOn({}, "summary") || L.isOn({}, "node")) return { FAIL: "defaults" };

  const local = window.k8sPrefs.safeStorage();
  const keep = ["k8sfoams.llm", "k8sfoams.llm.key"].map(k => [k, local.getItem(k), sessionStorage.getItem(k)]);
  try {
    L.saveConn({ mode: "own", url: "u", model: "m", remember: false, budget: 20000, key: "k1" });
    if (local.getItem("k8sfoams.llm.key") !== null || sessionStorage.getItem("k8sfoams.llm.key") !== '"k1"') return { FAIL: "an unremembered key reached localStorage" };
    L.saveConn({ mode: "own", url: "u", model: "m", remember: true, budget: 20000, key: "k2" });
    if (L.loadConn().key !== "k2" || sessionStorage.getItem("k8sfoams.llm.key") !== null) return { FAIL: "remember" };
    L.forgetKey();
    if (L.loadConn().key !== "") return { FAIL: "forget" };
  } finally {
    for (const [k, l, s] of keep) {
      if (l === null) local.removeItem(k); else local.setItem(k, l);
      if (s === null) sessionStorage.removeItem(k); else sessionStorage.setItem(k, s);
    }
  }
  return "ok";
})()
