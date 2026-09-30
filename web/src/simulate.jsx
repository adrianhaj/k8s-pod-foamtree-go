// Scheduling simulators. The server runs the scheduler maths on its cache;
// this file only asks and shows the answer.

const { useState } = React;
const { SevGlyph } = window.k8sIcons;

async function getJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
  return r.json();
}

// Rejected nodes dim, pods on the nodes that fit light up — the same match
// shape the query bar produces, so 2D and 3D need no changes.
function fitMatch(nodes, verdicts) {
  const dimNodes = new Set(verdicts.filter(v => v.reasons.length > 0).map(v => v.node));
  const pods = new Set();
  let total = 0;
  for (const n of nodes) {
    total += n.pods.length;
    if (!dimNodes.has(n.name)) n.pods.forEach(p => pods.add(p));
  }
  return { active: true, pods, dimNodes, count: pods.size, total, errors: [] };
}

// ponytail: the verdict is a snapshot; a refresh does not re-run it.
function FitForm({ context, onResult }) {
  const [form, setForm] = useState({ cpu: "500m", memory: "1Gi", nodeSelector: "", tolerations: "" });
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  const check = async (e) => {
    e.preventDefault();
    setBusy(true);
    try {
      onResult(await getJSON(`/api/fit?${new URLSearchParams({ ...form, context })}`));
      setError(null);
    } catch (err) {
      onResult(null);
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const field = (key, label, placeholder) => (
    <label className="sim-field">
      <span>{label}</span>
      <input value={form[key]} placeholder={placeholder} spellCheck="false"
        onChange={e => setForm({ ...form, [key]: e.target.value })} />
    </label>
  );

  return (
    <form className="sim-form sim-row" onSubmit={check}>
      {field("cpu", "CPU request", "500m")}
      {field("memory", "Memory request", "1Gi")}
      {field("nodeSelector", "Node selector", "disk=ssd,zone=a")}
      {field("tolerations", "Tolerations", "spot=true:NoSchedule")}
      <button className="btn-primary" type="submit" disabled={busy}>Check</button>
      {error && <div className="sim-error">{error}</div>}
    </form>
  );
}

// Why nodes said no, by reason kind, most common first.
function FitSummary({ result, onClear }) {
  const kinds = new Map();
  for (const v of result) for (const r of v.reasons) {
    const kind = r.split(":")[0];
    kinds.set(kind, (kinds.get(kind) || 0) + 1);
  }
  const fits = result.filter(v => v.reasons.length === 0).length;
  return (
    <div className="panel-bar">
      <span className="chip"><SevGlyph sev={fits ? "ok" : "danger"} />{fits} / {result.length} nodes fit</span>
      {[...kinds].sort((a, b) => b[1] - a[1]).map(([kind, n]) => <span key={kind} className="chip">{kind} · {n}</span>)}
      <button className="btn" onClick={onClear}>Clear</button>
    </div>
  );
}

// The focused node's verdict with numbers, e.g. "insufficient cpu: requires 4000m, available 1200m".
function FitVerdict({ reasons }) {
  return (
    <div className="overlay-sched">
      <div className="ov-section-title">Can I fit this pod?</div>
      {reasons.length === 0
        ? <span className="status-pill status-ready">fits</span>
        : reasons.map(r => <div key={r} className="sim-error">{r}</div>)}
    </div>
  );
}

// Pending first: that is the answer an operator is looking for.
function DrainResults({ result, podsByKey, fmtReq }) {
  const rows = [
    ...result.pending.map(p => ({ pod: p.pod, sev: "danger", label: "Pending", why: p.reason })),
    ...result.unmanaged.map(p => ({ pod: p, sev: "warn", label: "Not recreated", why: "no controller owns it" })),
    ...result.moved.map(p => ({ pod: p.pod, sev: null, label: "lands on", why: p.node })),
  ];
  const req = (key, metric) => { const p = podsByKey.get(key); return p ? fmtReq(p[metric], metric) : "–"; };
  return (
    <>
      <div className="panel-bar">
        <span className="chip"><SevGlyph sev="ok" />{result.moved.length} rescheduled</span>
        <span className="chip"><SevGlyph sev={result.pending.length ? "danger" : "ok"} />{result.pending.length} Pending</span>
        {result.unmanaged.length > 0 && <span className="chip"><SevGlyph sev="warn" />{result.unmanaged.length} not recreated</span>}
        {result.ignored.length > 0 && <span className="chip">{result.ignored.length} DaemonSet / static</span>}
      </div>
      <div className="ptable drain" role="table" aria-label="Drain simulation">
        <div className="ptr th" role="row"><span>Pod</span><span>CPU request</span><span>Memory request</span><span>Result</span></div>
        {rows.map(r => (
          <div key={r.pod} className="ptr" role="row">
            <span>{r.pod}</span><span>{req(r.pod, "cpu")}</span><span>{req(r.pod, "mem")}</span>
            <span>{r.sev && <SevGlyph sev={r.sev} />}<b>{r.label}</b><span className="mut">{r.why}</span></span>
          </div>
        ))}
      </div>
    </>
  );
}

window.k8sSimulate = { getJSON, fitMatch, FitForm, FitSummary, FitVerdict, DrainResults };
