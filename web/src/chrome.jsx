// Console chrome around the map: top bar now; summary strip, toolbar, rail and
// settings in the next task. Layout only: App owns the state.

const { useState, useEffect, useRef } = React;
const { Icon, SevGlyph } = window.k8sIcons;
const { shortContext, timeAgo } = window.k8sFormat;

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
  const { worstSeverity, warnInfo } = window.k8sNodeStatus;
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
  return ["danger", "warn", "info"].filter(s => by.has(s)).map(s => {
    const e = by.get(s);
    const [slug] = [...e.slugs].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))[0];
    return { sev: s, count: e.count, query: `health:${slug}` };
  });
}

// A <details> dropdown that closes on a pick, an outside click or Escape.
function Menu({ className = "", label, title, children }) {
  const ref = useRef(null);
  useEffect(() => {
    const close = e => {
      const d = ref.current;
      if (!d || !d.open) return;
      if (e.type === "keydown" ? e.key === "Escape" : !d.contains(e.target)) d.open = false;
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", close); };
  }, []);
  return (
    <details className={`menu ${className}`} ref={ref}>
      <summary title={title} aria-label={title}>{label}</summary>
      <div className="menu-pop" role="menu" onClick={e => { if (e.target.closest("[role^=menuitem]")) ref.current.open = false; }}>
        {children}
      </div>
    </details>
  );
}

const radio = (on, onPick, label, extra = null) => (
  <button role="menuitemradio" aria-checked={on} onClick={onPick}>
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
          {contexts.map((c, i) => (
            <React.Fragment key={c.context}>
              {radio(i === contextIdx, () => setContextIdx(i), shortContext(c.context), <span className="tag">{providerOf(c.context)}</span>)}
            </React.Fragment>
          ))}
        </Menu>
      )}
      {queryBar}
      <div className="topbar-right">
        <span className={`status${error ? " is-error" : ""}`} title={error || ""}>
          <i className="status-dot" />{error ? "Cluster unreachable" : `Updated ${timeAgo(lastRefresh)}`}
        </span>
        <Menu className="refresh-menu" title="Auto-refresh" label={<>Auto-refresh {refreshLabel(refreshInterval)}<Icon name="chev" size={12} /></>}>
          {REFRESH_CHOICES.map(([s, l]) => (
            <React.Fragment key={s}>{radio(s === refreshInterval, () => setRefreshInterval(s), l)}</React.Fragment>
          ))}
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

window.k8sChrome = { REFRESH_CHOICES, providerOf, refreshLabel, attentionBySev, Menu, TopBar, radio };
