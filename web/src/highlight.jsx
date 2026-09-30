// What the map lights up. A typed query always wins, so the query bar never
// disagrees with the map; otherwise the open panel tab decides. Every result
// has the match shape the map already reads, plus `ring` (a node name to
// outline, or null) and `lit` (why, for the map chip, or null).

function pickShown({ match, tab, changes, sim, nodes }) {
  const plain = { ...match, ring: null, lit: null };
  if (match.active) return plain;
  if (tab === "changes" && changes && changes.pods.size > 0) {
    return { ...plain, active: true, pods: changes.pods, dimNodes: new Set(), count: changes.pods.size, lit: "changes" };
  }
  if (tab === "drain" && sim) {
    if (sim.mode === "fit" && sim.fit) return { ...plain, ...sim.fit, errors: match.errors, lit: "fit" };
    if (sim.mode === "drain" && sim.node) {
      const node = nodes.find(n => n.name === sim.node);
      if (node) {
        return { ...plain, active: true, pods: new Set(node.pods), dimNodes: new Set(), count: node.pods.length, ring: node.name, lit: "drain" };
      }
    }
  }
  return plain;
}

window.k8sHighlight = { pickShown };
