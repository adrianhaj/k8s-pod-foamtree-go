// The docked bottom panel: Problems, Changes and Drain simulation. Layout and
// presentation only; App owns the state and hands each tab its data.

const { Icon, SevGlyph } = window.k8sIcons;
const { clock } = window.k8sFormat;

const TABS = [
  { id: "problems", label: "Problems" },
  { id: "pending", label: "Pending" },
  { id: "changes", label: "Changes" },
  { id: "drain", label: "Drain simulation" },
  { id: "logs", label: "Logs" },
  // The transcript needs the height: the Assistant opens maximized.
  { id: "assistant", label: "Assistant", max: true },
];
const showTab = (p, id) => ({ ...p, open: true, tab: id, max: p.max || !!TABS.find(t => t.id === id).max });
const ROW_CAP = 100; // ponytail: plain list; virtualise if clusters routinely exceed this

function BottomPanel({ panel, setPanel, counts, children }) {
  const pick = id => setPanel(p => showTab(p, id));
  const onKey = e => {
    const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (!d || e.target.getAttribute("role") !== "tab") return;
    const i = TABS.findIndex(t => t.id === panel.tab);
    const next = TABS[(i + d + TABS.length) % TABS.length].id;
    // Cycling only switches tabs; a click on a tab may also maximize it.
    setPanel(p => ({ ...p, open: true, tab: next }));
    const el = document.getElementById(`tab-${next}`);
    if (el) el.focus();
  };
  return (
    <section className={`panel${panel.open ? "" : " is-collapsed"}${panel.max && panel.open ? " is-max" : ""}`} aria-label="Details">
      <div className="panel-tabs" role="tablist" onKeyDown={onKey}>
        {TABS.map(t => (
          <button key={t.id} id={`tab-${t.id}`} role="tab" aria-selected={panel.tab === t.id} aria-controls="panel-body"
            tabIndex={panel.tab === t.id ? 0 : -1} className={panel.tab === t.id ? "on" : ""} onClick={() => pick(t.id)}>
            {t.label}{counts[t.id] ? <em>{counts[t.id]}</em> : null}
          </button>
        ))}
        <span className="panel-act">
          <button title={panel.max ? "Restore" : "Maximize"} aria-label={panel.max ? "Restore panel" : "Maximize panel"}
            onClick={() => setPanel(p => ({ ...p, open: true, max: !p.max }))}><Icon name="max" size={14} /></button>
          <button title={panel.open ? "Collapse" : "Expand"} aria-label={panel.open ? "Collapse panel" : "Expand panel"}
            onClick={() => setPanel(p => ({ ...p, open: !p.open }))}>
            <span className={panel.open ? "" : "flip"}><Icon name="chev" size={14} /></span>
          </button>
        </span>
      </div>
      {panel.open && (
        <div className="panel-body" id="panel-body" role="tabpanel" aria-labelledby={`tab-${panel.tab}`}>{children}</div>
      )}
    </section>
  );
}

// Chips toggle health:/audit: query tokens, so the map highlight and the
// query bar show the same thing.
function ProblemsTab({ rows, chips, query, setQuery, onPickNode }) {
  if (rows.length === 0) {
    return <div className="panel-empty">No problems found. Every node is schedulable and every pod passes the audit.</div>;
  }
  const q = query.trim();
  const active = chips.some(c => c.query === q) ? q : null;
  const shown = active ? rows.filter(r => r.query === active) : rows;
  return (
    <>
      <div className="panel-chips">
        <button className={q === "" ? "on" : ""} onClick={() => setQuery("")}>All <em>{rows.length}</em></button>
        {chips.map(c => (
          <button key={c.query} className={active === c.query ? "on" : ""} aria-pressed={active === c.query}
            onClick={() => setQuery(active === c.query ? "" : c.query)}>
            <SevGlyph sev={c.sev} />{c.label}<em>{c.count}</em>
          </button>
        ))}
      </div>
      <div className="ptable problems" role="table" aria-label="Problems">
        <div className="ptr th" role="row"><span /><span>Rule</span><span>Object</span><span>Node</span><span>Detail</span></div>
        {shown.slice(0, ROW_CAP).map((r, i) => (
          <button key={`${r.query}|${r.object}|${i}`} className="ptr" role="row" onClick={() => onPickNode(r.node)} title={`Open ${r.node}`}>
            <span><SevGlyph sev={r.sev} /></span><span>{r.rule}</span><span>{r.object}</span><span>{r.node}</span><span className="mut">{r.detail}</span>
          </button>
        ))}
        {shown.length > ROW_CAP && <div className="ptr more">+{shown.length - ROW_CAP} more. Pick a chip to narrow the list.</div>}
      </div>
    </>
  );
}

// Pods no node has taken, with the scheduler's own words. Nothing on the map
// to light, so rows are not buttons.
function PendingTab({ pods }) {
  if (pods.length === 0) return <div className="panel-empty">No pending pods. Every pod has a node.</div>;
  return (
    <div className="ptable pending" role="table" aria-label="Pending pods">
      <div className="ptr th" role="row"><span>Pod</span><span>Reason</span><span>Scheduler message</span></div>
      {pods.slice(0, ROW_CAP).map(p => (
        <div key={`${p.namespace}/${p.name}`} className="ptr" role="row" title={p.message}>
          <span title={`${p.namespace}/${p.name}`}>{p.namespace}/{p.name}</span><span>{p.reason || "Waiting"}</span><span className="mut">{p.message}</span>
        </div>
      ))}
      {pods.length > ROW_CAP && <div className="ptr more">+{pods.length - ROW_CAP} more</div>}
    </div>
  );
}

function ChangeCol({ sev, glyph, label, items }) {
  return (
    <div className="chg-col">
      <div className="chg-h"><SevGlyph sev={sev} glyph={glyph} />{label}<em>{items.length}</em></div>
      <div className="chg-list">
        {items.slice(0, ROW_CAP).map(s => <div key={s} className="chg-it" title={s}>{s}</div>)}
        {items.length > ROW_CAP && <div className="chg-it mut">+{items.length - ROW_CAP} more</div>}
      </div>
    </div>
  );
}

function ChangesTab({ entries, at, atLabel, onScrub, playing, onPlay, onLive, base, onCompare, baseLabel, lists }) {
  if (entries.length === 0) {
    return <div className="panel-empty">Recording starts with the first refresh. Changes appear once there are two snapshots.</div>;
  }
  const idx = at == null ? entries.length - 1 : entries.findIndex(e => e.t === at);
  return (
    <>
      <div className="panel-bar">
        <input type="range" id="history-scrub" aria-label="History" min="0" max={entries.length - 1} step="1"
          value={idx} onChange={e => onScrub(+e.target.value)} />
        <span>{atLabel} · {entries.length} snapshot{entries.length === 1 ? "" : "s"}</span>
        <div className="seg">
          <button className={playing ? "seg-on" : ""} onClick={onPlay}>{playing ? "Pause" : "Play"}</button>
          <button className={at == null ? "seg-on" : ""} onClick={onLive}>Live</button>
        </div>
        <button className="btn" onClick={onCompare}>{base ? "Stop comparing" : "Compare with this snapshot"}</button>
      </div>
      {base ? (
        <>
          <p className="panel-note">Compared with the snapshot from <b>{baseLabel}</b>. The map lights added and resized pods.</p>
          {lists ? (
            <div className="chg-cols">
              <ChangeCol sev="ok" glyph="+" label="Pods added" items={lists.added} />
              <ChangeCol sev="danger" glyph="−" label="Pods removed" items={lists.removed} />
              <ChangeCol sev="warn" glyph="~" label="Workloads resized" items={lists.resized} />
              <ChangeCol sev="info" glyph="Δ" label="Nodes changed" items={lists.nodes} />
            </div>
          ) : <div className="panel-empty">Loading snapshot…</div>}
        </>
      ) : (
        <div className="panel-empty">Pick a snapshot with the slider and press Compare. The comparison survives a context switch, so it also compares two contexts.</div>
      )}
    </>
  );
}

function DrainTab({ mode, setMode, node, setNode, nodes, busy, error, onRun, drainBody, fitBody }) {
  return (
    <>
      <div className="panel-bar">
        <div className="seg" role="radiogroup" aria-label="Simulation">
          <button role="radio" aria-checked={mode === "drain"} className={mode === "drain" ? "seg-on" : ""} onClick={() => setMode("drain")}>Drain a node</button>
          <button role="radio" aria-checked={mode === "fit"} className={mode === "fit" ? "seg-on" : ""} onClick={() => setMode("fit")}>Fit a pod</button>
        </div>
        {mode === "drain" && (
          <>
            <select id="drain-node" aria-label="Node to drain" value={node || ""} onChange={e => setNode(e.target.value || null)}>
              <option value="">Pick a node</option>
              {nodes.map(n => <option key={n.name} value={n.name}>{n.name}</option>)}
            </select>
            <button className="btn-primary" disabled={!node || busy} onClick={onRun}>Simulate drain</button>
            <span>Dry run. Nothing is evicted or cordoned.</span>
          </>
        )}
      </div>
      {mode === "fit" ? fitBody
        : error ? <div className="panel-empty sim-error">{error}</div>
        : drainBody || <div className="panel-empty">Pick a node to see where its pods would land. The map lights that node while you choose.</div>}
    </>
  );
}

const LIT_TEXT = { changes: "Lit: added and resized pods", fit: "Lit: nodes that fit", drain: null };

function MapChips({ lit, ring, litPod, workload, onClearWorkload, onLogs, at, onLive }) {
  const text = lit === "drain" ? `Lit: pods on ${ring}` : lit === "logs" ? `Lit: ${litPod}` : LIT_TEXT[lit];
  return (
    <div className="map-chips">
      {at != null && <span className="chip">History · {clock(at)}<button onClick={onLive}>Back to live</button></span>}
      {workload && (
        <span className="chip chip-wl" title={workload.key}>
          <span>{workload.key} · {workload.replicas} replica{workload.replicas === 1 ? "" : "s"} · {workload.nodes} node{workload.nodes === 1 ? "" : "s"}</span>
          <button className="chip-act" onClick={onLogs} title="Open logs for this workload">Logs</button>
          <button onClick={onClearWorkload} aria-label="Clear selection (Esc)">×</button>
        </span>
      )}
      {text && <span className="chip chip-lit">{text}</span>}
    </div>
  );
}

window.k8sPanel = { TAB_IDS: TABS.map(t => t.id), BottomPanel, ProblemsTab, PendingTab, ChangesTab, DrainTab, MapChips, showTab };
