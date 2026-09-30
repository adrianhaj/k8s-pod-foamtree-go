// The Problems tab's rows: one per node warning and one per pod finding. Pure
// data in and out. Labels and severities come from the nodestatus and podaudit
// vocabularies, so the tab, the badges and the query agree.

const PROBLEM_RANK = { danger: 3, warn: 2, info: 1 };
const PRESSURE = { "memory-pressure": "MemoryPressure", "disk-pressure": "DiskPressure", "pid-pressure": "PIDPressure" };
const cmp = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

function nodeDetail(node, slug) {
  if (slug === "cordoned") return "spec.unschedulable=true";
  if (slug === "not-ready") return "Ready condition is not True";
  if (PRESSURE[slug]) return `${PRESSURE[slug]}=True`;
  if (slug === "tainted") {
    // PreferNoSchedule never marks a node, so it is not a reason here either.
    return (node.taints || [])
      .filter(t => t.effect === "NoSchedule" || t.effect === "NoExecute")
      .map(t => `${t.key}${t.value ? "=" + t.value : ""}:${t.effect}`)
      .join(", ");
  }
  return slug;
}

function buildProblems(nodes) {
  const { warnInfo } = window.k8sNodeStatus;
  const { findingInfo } = window.k8sPodAudit;
  const rows = [];
  for (const n of nodes) {
    for (const w of n.warnings || []) {
      const info = warnInfo(w);
      rows.push({ sev: info.sev, rule: info.label, kind: "node", object: `node/${n.name}`, node: n.name, detail: nodeDetail(n, w), query: `health:${w}` });
    }
    for (const p of n.pods) {
      for (const f of p.findings || []) {
        const info = findingInfo(f);
        rows.push({ sev: info.sev, rule: info.label, kind: "pod", object: `${p.namespace}/${p.name}`, node: n.name, detail: info.why, query: `audit:${f}` });
      }
    }
  }
  return rows.sort((a, b) => PROBLEM_RANK[b.sev] - PROBLEM_RANK[a.sev]
    || (a.kind === b.kind ? 0 : a.kind === "node" ? -1 : 1)
    || cmp(a.rule, b.rule) || cmp(a.object, b.object));
}

// One toggle per rule present, worst first, then the most common.
function problemChips(rows) {
  const byQuery = new Map();
  for (const r of rows) {
    const c = byQuery.get(r.query) || { query: r.query, label: r.rule, sev: r.sev, count: 0 };
    c.count++;
    byQuery.set(r.query, c);
  }
  return [...byQuery.values()].sort((a, b) => PROBLEM_RANK[b.sev] - PROBLEM_RANK[a.sev] || b.count - a.count || cmp(a.label, b.label));
}

window.k8sProblems = { buildProblems, problemChips };
