// Console chrome around the map: top bar, summary strip, toolbar, rail and
// settings. Layout only: App owns the state.

const { useState, useEffect, useRef } = React;
const { Icon, SevGlyph } = window.k8sIcons;
const { shortContext, timeAgo, fmtMem, fmtCost } = window.k8sFormat;
const { utilTone, COLOR_MODES } = window.k8sPalette;
const { GROUP_BY } = window.k8sTopology;

const REFRESH_CHOICES = [[0, "Off"], [15, "15 s"], [30, "30 s"], [60, "1 min"], [300, "5 min"], [600, "10 min"]];

function providerOf(ctx) {
  return ctx.includes("eks") ? "eks" : ctx.includes("gke") ? "gke" : "k8s";
}

function refreshLabel(sec) {
  const c = REFRESH_CHOICES.find(([s]) => s === sec);
  return c ? c[1] : `${sec} s`;
}

// Nodes needing attention, one entry per severity present, worst first. Each
// carries the most common warning of its severity, which its glyph toggles.
function attentionBySev(nodes) {
  const { worstSeverity, warnInfo, SEV_RANK } = window.k8sNodeStatus;
  const by = new Map();
  for (const n of nodes) {
    const sev = worstSeverity(n.warnings);
    if (!sev) continue;
    const slug = n.warnings.find(w => warnInfo(w).sev === sev);
    const e = by.get(sev) || { count: 0, slugs: new Map() };
    e.count++;
    e.slugs.set(slug, (e.slugs.get(slug) || 0) + 1);
    by.set(sev, e);
  }
  return [...by].sort(([a], [b]) => SEV_RANK[b] - SEV_RANK[a]).map(([s, e]) => {
    const [slug] = [...e.slugs].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))[0];
    return { sev: s, count: e.count, query: `health:${slug}` };
  });
}

// A <details> dropdown that closes on a pick, an outside click or Escape.
function Menu({ className = "", label, title, children }) {
  const ref = useRef(null);
  const close = () => {
    const d = ref.current;
    if (d.contains(document.activeElement)) d.querySelector("summary").focus();
    d.open = false;
  };
  useEffect(() => {
    const onDoc = e => {
      const d = ref.current;
      if (!d || !d.open) return;
      if (e.type === "keydown" ? e.key === "Escape" : !d.contains(e.target)) close();
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onDoc);
    return () => { document.removeEventListener("mousedown", onDoc); document.removeEventListener("keydown", onDoc); };
  }, []);
  return (
    <details className={`menu ${className}`} ref={ref}>
      <summary title={title}>{label}</summary>
      <div className="menu-pop" role="menu" onClick={e => {
        if (e.target.closest("[role^=menuitem]")) close();
      }}>
        {children}
      </div>
    </details>
  );
}

const radio = (key, on, onPick, label, extra = null) => (
  <button key={key} role="menuitemradio" aria-checked={on} onClick={onPick}>
    <span className="menu-check">{on ? "✓" : ""}</span>{label}{extra}
  </button>
);

function TopBar({
  contexts, contextIdx, setContextIdx, queryBar, error, lastRefresh,
  refreshInterval, setRefreshInterval, onRefresh, refreshing, exportMenu, me,
}) {
  const [, tick] = useState(0);
  useEffect(() => { const id = setInterval(() => tick(t => t + 1), 5000); return () => clearInterval(id); }, []);
  const cur = contexts[contextIdx];
  return (
    <header className="topbar">
      <div className="tb-brand"><Icon name="logo" />k8sfoams</div>
      {cur && (
        <Menu className="ctx-picker" title="Switch context" label={<>
          <span className="ctx-k">Context</span><b>{shortContext(cur.context)}</b>
          <span className="tag">{providerOf(cur.context)}</span><Icon name="chev" size={12} />
        </>}>
          {contexts.map((c, i) => radio(c.context, i === contextIdx, () => setContextIdx(i), shortContext(c.context), <span className="tag">{providerOf(c.context)}</span>))}
        </Menu>
      )}
      {queryBar}
      <div className="topbar-right">
        <span className={`status${error ? " is-error" : ""}`} title={error || ""}>
          <i className="status-dot" /><span className="status-text">{error ? "Cluster unreachable" : `Updated ${timeAgo(lastRefresh)}`}</span>
        </span>
        <Menu className="refresh-menu" title="Auto-refresh" label={<>Auto-refresh {refreshLabel(refreshInterval)}<Icon name="chev" size={12} /></>}>
          {REFRESH_CHOICES.map(([s, l]) => radio(s, s === refreshInterval, () => setRefreshInterval(s), l))}
        </Menu>
        <button className={`icon-btn${refreshing ? " spinning" : ""}`} onClick={onRefresh} title="Refresh now" aria-label="Refresh now">
          <Icon name="refresh" size={14} />
        </button>
        {exportMenu}
        {me && me.auth === "oidc" && (
          <form method="post" action="/auth/logout">
            <button className="icon-btn" type="submit" title={`Sign out ${me.email || me.name}`} aria-label="Sign out"><Icon name="signout" size={14} /></button>
          </form>
        )}
      </div>
    </header>
  );
}

// One summary cell: label + figure, value, indicator. The fixed third row keeps
// every indicator on one line across cells, clear of the toolbar (spec §2).
function Metric({ label, u, value, of, title }) {
  const tone = utilTone(u);
  return (
    <div className="metric" title={title}>
      <div className="metric-l">{label}<b>{Math.round(u * 100)}%</b></div>
      <div className="metric-v">{value} <small>{of}</small></div>
      <div className="metric-bar"><i className={tone ? `tone-${tone}` : ""} style={{ width: `${Math.min(100, u * 100)}%` }} /></div>
    </div>
  );
}

function SummaryStrip({ totals, memUnit, qosBreakdown, attention, extCell, query, setQuery, largest, idle }) {
  const q = query.trim();
  const toggle = token => setQuery(q === token ? "" : token);
  const be = qosBreakdown.find(x => x.qos === "BestEffort");
  const needy = attention.reduce((s, a) => s + a.count, 0);
  const fits = largest && largest.cpu >= 50; // under 50m prints "0.0 c"
  return (
    <section className="summary" aria-label="Cluster totals">
      <Metric label="CPU requested" u={totals.cpuUsed / (totals.cpuCap || 1)}
        value={(totals.cpuUsed / 1000).toFixed(1)} of={`of ${(totals.cpuCap / 1000).toFixed(1)} cores`} />
      <Metric label="Memory requested" u={totals.memUsed / (totals.memCap || 1)}
        value={fmtMem(totals.memUsed, memUnit)} of={`of ${fmtMem(totals.memCap, memUnit, true)} ${memUnit}`} />
      {extCell && <Metric {...extCell} />}
      {idle && <Metric label="Cost idle" u={idle.idle / (idle.total || 1)} value={fmtCost(idle.idle)} of={`of ${fmtCost(idle.total)}`}
        title={`Node price no pod request is charged for${idle.unpriced ? `; leaves out ${idle.unpriced} unpriced nodes` : ""}`} />}
      <div className="metric">
        <div className="metric-l">Pods by QoS<b>{be ? be.count : 0} BestEffort</b></div>
        <div className="metric-v">{totals.pods} <small>of {totals.nodes * 110} slots</small></div>
        <div className="qos-bar">
          {qosBreakdown.filter(x => x.count > 0).map(x => (
            <button key={x.qos} className={`sev-${x.sev}`} style={{ flexGrow: x.count }} title={`${x.label}: ${x.count}. ${x.risk}`}
              aria-label={`${x.label}: ${x.count} pods`} aria-pressed={q === `qos:${x.qos}`} onClick={() => toggle(`qos:${x.qos}`)} />
          ))}
        </div>
      </div>
      <div className="metric">
        <div className="metric-l">Nodes{largest && (
          <b title={fits ? `Largest pod at the median pod shape that fits on a node with no warnings (${largest.node})` : "No node with no warnings has room for a pod of the median shape"}>
            {fits ? `fits ${(largest.cpu / 1000).toFixed(1)} c · ${fmtMem(largest.mem, memUnit)} ${memUnit}` : "full"}
          </b>
        )}</div>
        <div className="metric-v">{totals.nodes} <small>· {needy ? `${needy} need attention` : "all schedulable"}</small></div>
        <div className="metric-glyphs">
          {attention.map(a => (
            <button key={a.sev} aria-label={`${a.count} nodes, ${a.query}`} aria-pressed={q === a.query} onClick={() => toggle(a.query)} title={`Highlight ${a.query}`}>
              <SevGlyph sev={a.sev} />{a.count}
            </button>
          ))}
        </div>
      </div>
    </section>
  );
}

function Toolbar({ view, metric, metrics, setMetric, zoom, setZoom, groupBy, setGroupBy, showGroupBy, colorBy, setColorBy, legend }) {
  return (
    <div className="toolbar">
      {view === "3d" ? (
        <label className="tb-f" htmlFor="zoom">Zoom
          <input type="range" id="zoom" min="0.4" max="1.6" step="0.05" value={zoom} onChange={e => setZoom(+e.target.value)} />
          <span className="mono">{Math.round(zoom * 100)}%</span>
        </label>
      ) : (
        <div className="tb-f">Size by
          <div className="seg" role="radiogroup" aria-label="Size by">
            {metrics.map(m => (
              <button key={m.id} role="radio" aria-checked={metric === m.id} className={metric === m.id ? "seg-on" : ""} onClick={() => setMetric(m.id)}>{m.label}</button>
            ))}
          </div>
        </div>
      )}
      {showGroupBy && (
        <label className="tb-f" htmlFor="group-by">Group by
          <select id="group-by" value={groupBy} onChange={e => setGroupBy(e.target.value)}>
            {GROUP_BY.map(g => <option key={g.id} value={g.id}>{g.label}</option>)}
          </select>
        </label>
      )}
      <label className="tb-f" htmlFor="color-by">Color by
        <select id="color-by" value={colorBy} onChange={e => setColorBy(e.target.value)}>
          {COLOR_MODES.map(c => <option key={c.id} value={c.id}>{c.label}</option>)}
        </select>
      </label>
      <div className="legend">{legend}</div>
    </div>
  );
}

function SettingsMenu({ themePref, setThemePref, memUnit, setMemUnit, onAssistant }) {
  return (
    <Menu className="settings" title="Settings" label={<Icon name="gear" />}>
      <div className="menu-h">Theme</div>
      {[["system", "System"], ["light", "Light"], ["dark", "Dark"]].map(([id, l]) => radio(id, themePref === id, () => setThemePref(id), l))}
      <div className="menu-h">Memory unit</div>
      {["MiB", "GiB", "TiB"].map(u => radio(u, memUnit === u, () => setMemUnit(u), u))}
      <div className="menu-h">Assistant</div>
      <button role="menuitem" onClick={onAssistant}><span className="menu-check" />Connection…</button>
    </Menu>
  );
}

// Floats over the map's left edge. The panel buttons also work while the panel
// is collapsed: pressing the open tab's button collapses it again.
function Rail({ view, setView, panel, openTab, findings, settings }) {
  const viewBtn = (id, icon, label) => (
    <button className={view === id ? "on" : ""} aria-pressed={view === id} title={label} aria-label={label} onClick={() => setView(id)}>
      <Icon name={icon} />
    </button>
  );
  const tabBtn = (id, icon, label, badge) => {
    const on = panel.open && panel.tab === id;
    return (
      <button className={on ? "on" : ""} aria-pressed={on} title={label} aria-label={label} onClick={() => openTab(id)}>
        <Icon name={icon} />{badge ? <span className="badge">{badge}</span> : null}
      </button>
    );
  };
  return (
    <nav className="rail" aria-label="Views and panels">
      {viewBtn("2d", "logo", "Map")}
      {viewBtn("3d", "cube", "3D view")}
      <span className="rail-div" />
      {tabBtn("problems", "alert", "Problems", findings)}
      {tabBtn("changes", "clock", "Changes")}
      {tabBtn("drain", "flask", "Drain simulation")}
      {tabBtn("logs", "terminal", "Logs")}
      {tabBtn("assistant", "chat", "Assistant")}
      <span className="rail-div" />
      {settings}
    </nav>
  );
}

window.k8sChrome = { TopBar, SummaryStrip, Toolbar, Rail, SettingsMenu, attentionBySev };
