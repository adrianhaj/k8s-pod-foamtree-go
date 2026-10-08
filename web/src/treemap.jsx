// Squarified treemap. Pure function: given a rect + items[{value,...}],
// returns items with x/y/w/h placed.

const { workloadKey } = window.k8sWorkload;
const { worstSeverity, NodeWarnBadge } = window.k8sNodeStatus;
const { PodAuditBadge } = window.k8sPodAudit;
const { podToken, utilTone } = window.k8sPalette;

function squarify(items, x, y, w, h) {
  const sorted = items.filter(i => i.value > 0).sort((a, b) => b.value - a.value);
  if (!sorted.length || w <= 0 || h <= 0) return [];
  const total = sorted.reduce((s, i) => s + i.value, 0);
  const scale = (w * h) / total;
  const scaled = sorted.map(i => ({ ...i, _a: i.value * scale }));

  const out = [];
  let cx = x, cy = y, cw = w, ch = h;
  let rest = scaled;

  while (rest.length) {
    const short = Math.min(cw, ch);
    if (short <= 0.5) break;

    let row = [];
    let worst = Infinity;

    const evalRow = (r) => {
      const sum = r.reduce((s, i) => s + i._a, 0);
      let max = -Infinity, min = Infinity;
      for (const i of r) { if (i._a > max) max = i._a; if (i._a < min) min = i._a; }
      const s2 = short * short;
      const sum2 = sum * sum;
      return Math.max((s2 * max) / sum2, sum2 / (s2 * min));
    };

    for (let i = 0; i < rest.length; i++) {
      const trial = [...row, rest[i]];
      const r = evalRow(trial);
      if (r > worst) break;
      row = trial; worst = r;
    }
    if (!row.length) row = [rest[0]];

    const sum = row.reduce((s, i) => s + i._a, 0);
    const along = sum / short;

    let off = 0;
    if (cw >= ch) {
      for (const r of row) {
        const h2 = r._a / along;
        out.push({ ...r, x: cx, y: cy + off, w: along, h: h2 });
        off += h2;
      }
      cx += along; cw -= along;
    } else {
      for (const r of row) {
        const w2 = r._a / along;
        out.push({ ...r, x: cx + off, y: cy, w: w2, h: along });
        off += w2;
      }
      cy += along; ch -= along;
    }
    rest = rest.slice(row.length);
  }
  return out;
}

// Pods and containers carry CPU, memory and cost, plus extended requests
// (GPUs, ephemeral-storage, hugepages) under ext; any of them can be the metric.
function metricValue(o, metric) {
  return metric === "cpu" ? o.cpu : metric === "mem" ? o.mem : metric === "cost" ? (o.cost || 0) : (o.ext[metric] || 0);
}

function metricCap(node, metric) {
  return metric === "cpu" ? node.cpuCapacity : metric === "mem" ? node.memCapacity : metric === "cost" ? (node.cost || 0) : (node.ext[metric] || 0);
}

// The pods and free space a node card lays out; the SVG export uses it too.
function cardItems(node, metric, podMatched) {
  // Compute pod values + empty space. Size each pod by its effective request
  // (max(sum regular, max init)) — summing containers would double-count
  // init containers, which run sequentially before the regular ones.
  const podValue = p => metricValue(p, metric);
  const cap = metricCap(node, metric);
  const used = node.pods.reduce((s, p) => s + podValue(p), 0);
  // A pod requesting nothing on this metric (every BestEffort pod, by
  // definition) weighs 0 and squarify's `value > 0` guard drops it — so a
  // matched one would raise the header count while the card drew nothing.
  // Floor those to a thin sliver so the 2D and 3D views agree on what a query
  // highlights. Unmatched pods keep their real value, layout untouched.
  const sliver = cap * 0.002;
  const podItems = node.pods.map(p => ({
    pod: p,
    value: podValue(p) > 0 ? podValue(p) : (podMatched(p) ? sliver : 0),
  }));
  const empty = Math.max(0, cap - used);
  return { items: [...podItems, { pod: null, value: empty, empty: true }], cap, used, empty };
}

// Render a node card: header + nested treemap of pods (each pod = treemap of containers).
function NodeCard({
  node, match, metric, colorBy, nsMap, fmtReq, onClick,
  highlight, highlightActive, onPodSelect, onPodHover,
}) {
  const ref = React.useRef(null);
  const [box, setBox] = React.useState({ w: 0, h: 0 });

  React.useLayoutEffect(() => {
    if (!ref.current) return;
    const el = ref.current;
    const ro = new ResizeObserver(() => setBox({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    setBox({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  // Query dimming. A node ruled out by a node: glob dims as a whole card, so
  // its pods stay at full strength inside it rather than fading twice.
  const queryActive = !!(match && match.active);
  const nodeDim = queryActive && match.dimNodes.has(node.name);
  const podMatched = pod => queryActive && match.pods.has(pod);

  const padding = 4;
  const headerH = 24;
  const { items, cap, used, empty } = cardItems(node, metric, podMatched);
  const innerW = Math.max(0, box.w - padding * 2);
  const innerH = Math.max(0, box.h - headerH - padding);
  // Memoised: a highlight change re-renders every card, and hovering a pod must
  // not redo the layout of every card.
  const laid = React.useMemo(
    () => (innerW > 0 && innerH > 0 ? squarify(items, padding, headerH, innerW, innerH) : []),
    [node, metric, match, innerW, innerH]
  );

  // Free capacity on a node that refuses pods is not really free, so the idle
  // foam gets hatched in the worst warning's colour instead of the neutral one.
  const warnSev = worstSeverity(node.warnings);
  const utilization = used / cap;
  const tone = utilTone(utilization);

  return (
    <div ref={ref} onClick={onClick} className={`node-card${nodeDim ? " is-dim" : ""}${match && match.ring === node.name ? " is-ring" : ""}`}>
      <div className="node-header" style={{ height: headerH }}>
        <span className="node-name">{node.name}</span>
        {node.instanceType && <span className="node-type">{node.instanceType}</span>}
        <span className="node-meta">
          <NodeWarnBadge warnings={node.warnings} />
          <span className="node-ubar"><i className={tone ? `tone-${tone}` : ""} style={{ width: `${Math.min(100, utilization * 100)}%` }} /></span>
          <span className={`node-util${tone ? ` tone-${tone}` : ""}`}>{Math.round(utilization * 100)}%</span>
        </span>
      </div>
      {laid.map((it, i) => it.empty ? (
        <div key={`empty-${i}`} className={`pod-empty${warnSev ? ` warn-${warnSev}` : ""}`}
          style={{ left: it.x, top: it.y, width: it.w - 2, height: it.h - 2 }}>
          {it.w > 60 && it.h > 30 && <span>idle · {metric === "cost" ? fmtReq(empty, metric) : `${Math.round((empty / cap) * 100)}%`}</span>}
        </div>
      ) : (
        <PodBox key={`pod-${i}`} pod={it.pod} rect={it} role={podToken(it.pod, colorBy, nsMap)}
          metric={metric} fmtReq={fmtReq}
          matched={podMatched(it.pod)} dim={queryActive && !nodeDim && !podMatched(it.pod)}
          highlight={highlight} highlightActive={highlightActive}
          onPodSelect={onPodSelect} onPodHover={onPodHover} />
      ))}
    </div>
  );
}

function PodBox({
  pod, rect, role, metric, fmtReq, matched, dim,
  highlight, highlightActive, onPodSelect, onPodHover,
}) {
  const wl = workloadKey(pod.name);
  const cls = ["pod-box"];
  if (dim) cls.push("is-dim");
  if (matched) cls.push("is-match");
  if (highlight) {
    cls.push(wl === highlight ? "wl-peer" : "wl-dim");
    if (!highlightActive) cls.push("wl-preview");
  }
  const inset = 2;
  // Labels read top-left like a table cell: name, then the request beneath.
  const showName = rect.w >= 52 && rect.h >= 22;
  const showReq = showName && rect.h >= 38;
  const headerH = showReq ? 26 : showName ? 13 : 0;
  const laid = React.useMemo(() => squarify(
    pod.containers.map(c => ({ container: c, value: metricValue(c, metric) })),
    inset, headerH + inset, Math.max(0, rect.w - inset * 2), Math.max(0, rect.h - headerH - inset * 2)
  ), [pod, metric, rect, headerH]);

  return (
    <div className={cls.join(" ")}
      onClick={e => { e.stopPropagation(); onPodSelect(wl); }}
      onMouseEnter={() => onPodHover(wl)}
      onMouseLeave={() => onPodHover(null)}
      style={{ left: rect.x, top: rect.y, width: rect.w - 2, height: rect.h - 2, "--pod-c": `var(${role})` }}>
      {showName && <div className="pod-label">{pod.shortName}</div>}
      {showReq && <div className="pod-req">{fmtReq(metricValue(pod, metric), metric)}</div>}
      {rect.w > 24 && rect.h > 16 && <PodAuditBadge findings={pod.findings} />}
      {laid.map((it, i) => (
        <div key={i} className={`container-box${it.container.init ? " is-init" : ""}`}
          style={{ left: it.x, top: it.y, width: Math.max(0, it.w - 1), height: Math.max(0, it.h - 1) }}>
          {it.w > 40 && it.h > 18 && <span>{it.container.name}</span>}
        </div>
      ))}
    </div>
  );
}

window.k8sTreemap = { squarify, cardItems, NodeCard, PodBox, metricCap, metricValue };
