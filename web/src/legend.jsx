// Legend for the Color by modes. Swatches paint with the same tokens and tint
// as the pods they explain, so the legend is right in both themes.

function Legend({ colorBy, nsMap, view }) {
  const { QOS_ROLE } = window.k8sPalette;
  const sw = (key, token, label) => (
    <span key={key} className="lg"><i style={{ "--pod-c": `var(${token})` }} />{label}</span>
  );
  let items;
  if (colorBy === "qos") {
    items = window.k8sQos.QOS_ORDER.map(q => sw(q, QOS_ROLE[q], q));
  } else if (colorBy === "problems") {
    const rules = sev => Object.entries(window.k8sPodAudit.POD_FINDINGS).filter(([slug, f]) => f.sev === sev && !window.k8sPodAudit.isRuleDisabled(slug)).map(([, f]) => f.label).join(", ");
    items = [sw("danger", "--danger", rules("danger")), sw("warn", "--warn", rules("warn")), sw("info", "--info", rules("info")), sw("none", "--pod-neutral", "clean")];
  } else {
    items = [...nsMap].sort((a, b) => a[1] - b[1]).map(([ns, slot]) => sw(ns, `--ns-${slot}`, ns));
    items.push(sw("other", "--ns-other", "other"));
  }
  return <>{items}{view === "2d" && <span className="lg"><i className="hat" />idle</span>}</>;
}

window.k8sLegend = { Legend };
