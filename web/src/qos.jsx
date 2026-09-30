// QoS vocabulary. The backend passes the pod's status.qosClass through
// untouched; this file decides how each class looks, so the pod boxes, the
// cubes and the sidebar panel agree.

// Ordered by eviction risk, highest first — the order the sidebar lists them.
// `sev` picks the matching swatch class.
const QOS_INFO = {
  BestEffort: { sev: "danger", label: "BestEffort", risk: "no requests or limits — evicted first under memory pressure" },
  Burstable:  { sev: "warn",   label: "Burstable",  risk: "requests below limits — evicted after BestEffort" },
  Guaranteed: { sev: "ok",     label: "Guaranteed", risk: "requests equal limits — evicted last" },
};

const QOS_ORDER = Object.keys(QOS_INFO);

window.k8sQos = { QOS_INFO, QOS_ORDER };
