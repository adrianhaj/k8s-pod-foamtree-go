// Node topology grouping shared by the 2D map and the 3D scene, plus the cost
// totals over it. Pure data: the keys are the node fields the backend fills
// from well-known labels.

const GROUP_BY = [
  { id: "none", label: "None" },
  { id: "zone", label: "Zone", missing: "no zone" },
  { id: "region", label: "Region", missing: "no region" },
  { id: "pool", label: "Pool", missing: "no pool" },
  { id: "instanceType", label: "Type", missing: "no instance type" },
  { id: "capacityType", label: "Capacity", missing: "no capacity type" },
];

// [{ key, members: [{ node }] }] sorted by key; "none" is one group
// with an empty key.
function groupNodes(nodes, groupBy) {
  const opt = GROUP_BY.find(g => g.id === groupBy) || GROUP_BY[0];
  const groups = new Map();
  nodes.forEach(node => {
    const key = opt.id === "none" ? "" : node[opt.id] || opt.missing;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push({ node });
  });
  return [...groups].sort(([a], [b]) => a.localeCompare(b)).map(([key, members]) => ({ key, members }));
}

// Requested share of the group's capacity per resource — what shows a zone
// or pool running hotter than its siblings.
function groupUsage(members) {
  const t = { cpuUsed: 0, cpuCap: 0, memUsed: 0, memCap: 0 };
  for (const { node } of members) {
    t.cpuUsed += node.cpuUsed;
    t.cpuCap += node.cpuCapacity;
    t.memUsed += node.memUsed;
    t.memCap += node.memCapacity;
  }
  return { cpu: t.cpuUsed / (t.cpuCap || 1), mem: t.memUsed / (t.memCap || 1) };
}

// Price of the capacity no pod is charged for: each priced node's price minus
// its pods' costs, so idle and requested add up to the node price. Pods of
// opposite shapes can overpay a node, which then counts as 0 idle, not less.
// null when no node is priced.
function idleCost(nodes) {
  const priced = nodes.filter(n => n.cost != null);
  if (priced.length === 0) return null;
  const t = { idle: 0, total: 0, unpriced: nodes.length - priced.length };
  for (const n of priced) {
    t.idle += Math.max(0, n.cost - n.pods.reduce((s, p) => s + (p.cost || 0), 0));
    t.total += n.cost;
  }
  return t;
}

// Pods on nodes totalled by namespace, or by a label's value when key is set
// (pods without it share the "" group), costliest first: /report.csv?groupBy=
// for the snapshot on screen. cost is null while no pod in a group is priced.
function showback(nodes, key) {
  const groups = new Map();
  for (const n of nodes) {
    for (const p of n.pods) {
      const g = key == null ? p.namespace : Object.hasOwn(p.labels, key) ? p.labels[key] : "";
      if (!groups.has(g)) groups.set(g, { group: g, pods: 0, unpriced: 0, cpu: 0, mem: 0, cost: null });
      const r = groups.get(g);
      r.pods++;
      r.cpu += p.cpu;
      r.mem += p.mem;
      if (p.cost == null) r.unpriced++;
      else r.cost = (r.cost || 0) + p.cost;
    }
  }
  return [...groups.values()].sort((a, b) => (b.cost ?? -1) - (a.cost ?? -1) || a.group.localeCompare(b.group));
}

// Median MiB per millicore over pods that request both: the shape free
// capacity is judged against. null when no pod requests both.
function podShape(nodes) {
  const r = nodes.flatMap(n => n.pods.filter(p => p.cpu > 0 && p.mem > 0).map(p => p.mem / p.cpu)).sort((a, b) => a - b);
  if (r.length === 0) return null;
  const m = r.length >> 1;
  return r.length % 2 ? r[m] : (r[m - 1] + r[m]) / 2;
}

// Free capacity a pod of that shape cannot use: whatever is left on the axis
// that runs out second. At most one axis is non-zero.
function stranded(node, shape) {
  if (shape == null) return { cpu: 0, mem: 0 };
  const c = node.cpuFree, m = node.memFree;
  return { cpu: c - Math.min(c, m / shape), mem: m - Math.min(m, c * shape) };
}

// Largest pod of that shape still fitting on a node with no warnings.
// ponytail: capacity minus requests, like the empty foam; the backend sends no
// allocatable, so this overstates by system reservations. Serve allocatable
// on NodeGroup if users act on the number.
function largestFit(nodes, shape) {
  if (shape == null) return null;
  let best = null;
  for (const n of nodes) {
    if (n.warnings.length > 0) continue;
    const cpu = Math.min(n.cpuFree, n.memFree / shape);
    if (!best || cpu > best.cpu) best = { node: n.name, cpu, mem: cpu * shape };
  }
  return best;
}

// Joins metrics-server readings onto pods as pod.share (CPU use over CPU request).
// A pod with no reading gets null, which is not the same as a reading of zero.
function withUsage(nodes, res) {
  for (const n of nodes) for (const p of n.pods) {
    const u = res?.available ? res.pods[`${p.namespace}/${p.name}`] : null;
    p.share = u && p.cpu > 0 ? u.cpu / p.cpu : null;
  }
  return nodes;
}

window.k8sTopology = { GROUP_BY, groupNodes, groupUsage, idleCost, showback, podShape, stranded, largestFit, withUsage };
