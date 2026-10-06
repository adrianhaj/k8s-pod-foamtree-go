// Pod audit vocabulary. The backend decides *which* best-practice rules a pod
// breaks and sends slugs; this file is the single place that decides how each
// slug looks, so the pod badge, the Problems tab and the query agree.

const { SEV_RANK } = window.k8sNodeStatus;

function monolithWhy(share) {
  return `reserves over ${+(share * 100).toFixed(1)}% of its node — nowhere else to reschedule it`;
}

// Ordered the way the backend emits findings.
const POD_FINDINGS = {
  "missing-requests": { label: "missing requests", sev: "warn", why: "no CPU or memory request — the scheduler packs it blind" },
  "missing-limits":   { label: "no memory limit",  sev: "info", why: "no memory limit — a leak can take the node down" },
  "monolith":         { label: "monolith",         sev: "warn", why: monolithWhy(0.8) },
  "ratio-asymmetry":  { label: "ratio asymmetry",  sev: "info", why: "CPU:memory ratio far from the node's — strands the other resource" },
  "crashloop":  { label: "crash loop", sev: "danger", why: "a container keeps crashing and Kubernetes is backing off restarts", status: true },
  "oom-killed": { label: "OOM killed", sev: "danger", why: "a container's last run was killed for running out of memory", status: true },
  "image-pull": { label: "image pull", sev: "warn",   why: "a container image cannot be pulled", status: true },
  "resize-deferred":   { label: "resize deferred",   sev: "info", why: "in-place resize waits for room on its node — the scheduler already reserves the larger request" },
  "resize-infeasible": { label: "resize infeasible", sev: "warn", why: "in-place resize can never fit its node — it stays on the old requests" },
};

const FINDING_ORDER = Object.keys(POD_FINDINGS);

// Rules the server was started with --audit-disable for. They never appear in
// findings, so the query and the Problems tab say so instead of looking clean.
let disabledRules = [];

// The server's audit flags arrive with every tree; the copy follows them.
function configureAudit(audit) {
  if (!audit) return;
  POD_FINDINGS.monolith.why = monolithWhy(audit.monolithShare);
  disabledRules = audit.disabled || [];
}

function isRuleDisabled(slug) {
  return disabledRules.indexOf(slug) !== -1;
}

function disabledRuleCount() {
  return disabledRules.length;
}

function findingInfo(slug) {
  // An unknown slug from a newer backend still renders, just without a nicer
  // label — better a plain chip than a finding that silently vanishes.
  return POD_FINDINGS[slug] || { label: slug, sev: "warn", why: slug };
}

function findingsTitle(findings) {
  return (findings || []).map(f => findingInfo(f).why).join("\n");
}

// Worst wins, using the same ranking as the node badges.
function worstFindingSeverity(findings) {
  let worst = null;
  for (const f of findings || []) {
    const sev = findingInfo(f).sev;
    if (!worst || SEV_RANK[sev] > SEV_RANK[worst]) worst = sev;
  }
  return worst;
}

// Corner glyph on a pod box; the reasons live in the native tooltip.
function PodAuditBadge({ findings, size = 9 }) {
  const sev = worstFindingSeverity(findings);
  if (!sev) return null;
  const { WarnIcon } = window.k8sNodeStatus;
  return (
    <span className={`pod-audit sev-${sev}`} title={findingsTitle(findings)}>
      <WarnIcon size={size} />
    </span>
  );
}

function FindingPill({ f }) {
  const info = findingInfo(f);
  return (
    <span className={`audit-pill sev-${info.sev}`} title={info.why}>
      <PodAuditBadge findings={[f]} size={10} />
      {info.label}
    </span>
  );
}

window.k8sPodAudit = { POD_FINDINGS, FINDING_ORDER, configureAudit, isRuleDisabled, disabledRuleCount, findingInfo, findingsTitle, worstFindingSeverity, PodAuditBadge, FindingPill };
