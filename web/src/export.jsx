// export.jsx — image and report downloads from the header's Export menu.
//
// The 2D SVG is drawn from the data with the treemap's own squarify layout and
// colours instead of rasterising the page: it stays vector for decks, needs no
// library, and the PNG is that SVG drawn onto a canvas. It mirrors the colours
// and states of NodeCard and PodBox in treemap.jsx, so a change to those belongs
// in both; the node header omits the instance type and mini utilization bar, and
// pods omit the request line.

const { squarify, cardItems, metricCap, metricValue } = window.k8sTreemap;
const { worstSeverity, WarnIcon } = window.k8sNodeStatus;
const { worstFindingSeverity } = window.k8sPodAudit;
const { token } = window.k8sScene3D;
const { workloadKey } = window.k8sWorkload;
const { podToken, utilTone, tintHex } = window.k8sPalette;

const SCALE = 2;

// SVG text does not ellipsize; monospace glyphs are ~0.6em wide.
function fit(text, px, width) {
  const n = Math.floor(width / (px * 0.6));
  return text.length <= n ? text : n > 1 ? text.slice(0, n - 1) + "…" : "";
}

// Colours come from the live tokens at export time, so the file matches the
// theme on screen. SVG gets plain hex: not every consumer reads CSS colours.
function exportLook() {
  const t = name => token(name, "#808080");
  const pct = name => parseFloat(token(name, "20%")) / 100;
  return {
    bg: t("--bg"), panel: t("--panel"), line: t("--line"), text: t("--text"), dim: t("--text-dim"),
    accent: t("--accent"),
    tint: { fill: pct("--tint-fill"), edge: pct("--tint-edge"), box: pct("--tint-box") },
    sev: { danger: t("--danger"), warn: t("--warn"), info: t("--info") },
    hatch: {
      none: [t("--hatch-0"), t("--hatch-1"), t("--text-dim")],
      danger: [t("--hatch-danger-0"), t("--hatch-danger-1"), t("--hatch-danger-text")],
      warn: [t("--hatch-warn-0"), t("--hatch-warn-1"), t("--hatch-warn-text")],
      info: [t("--hatch-info-0"), t("--hatch-info-1"), t("--hatch-info-text")],
    },
    role: t,
    font: token("--font-mono", "ui-monospace, monospace"),
  };
}

function TreemapSVG({ nodes, width, height, metric, colorBy, nsMap, match, highlight, highlightActive }) {
  const pad = 14; // .grid-wrap padding
  const slots = squarify(
    nodes.map((node, idx) => ({ node, value: metricCap(node, metric) })),
    pad, pad, width - pad * 2, height - pad * 2
  );
  const look = { ...exportLook(), queryActive: !!(match && match.active), match, highlight, pinned: highlightActive, colorBy, nsMap };
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width={width} height={height} viewBox={`0 0 ${width} ${height}`} fontFamily={look.font}>
      <defs>
        {Object.entries(look.hatch).map(([k, [a, b]]) => (
          <pattern key={k} id={`hatch-${k}`} width={8} height={8} patternUnits="userSpaceOnUse" patternTransform="rotate(-45)">
            <rect width={5} height={8} fill={a} />
            <rect x={5} width={3} height={8} fill={b} />
          </pattern>
        ))}
      </defs>
      <rect width={width} height={height} fill={look.bg} />
      {slots.map(s => <SvgCard key={s.node.id} slot={s} metric={metric} look={look} />)}
    </svg>
  );
}

function SvgCard({ slot, metric, look }) {
  const { node } = slot;
  const w = slot.w - 6, h = slot.h - 6, padding = 4, headerH = 24;
  const nodeDim = look.queryActive && look.match.dimNodes.has(node.name);
  const podMatched = pod => look.queryActive && look.match.pods.has(pod);
  const { items, cap, used, empty } = cardItems(node, metric, podMatched);
  const innerW = w - 2 - padding * 2, innerH = h - 2 - headerH - padding;
  const laid = innerW > 0 && innerH > 0 ? squarify(items, padding, headerH, innerW, innerH) : [];
  const warnSev = worstSeverity(node.warnings);
  const util = used / cap, tone = utilTone(util);
  const utilText = `${Math.round(util * 100)}%`;
  const inner = w - 2, utilX = inner - 8;
  const badgeX = utilX - utilText.length * 6.6 - 6 - 11;
  const nameEnd = (warnSev ? badgeX : utilX - utilText.length * 6.6) - 8;
  const [, , hatchText] = look.hatch[warnSev || "none"];

  return (
    <g opacity={nodeDim ? 0.32 : 1}>
      <rect x={slot.x + 0.5} y={slot.y + 0.5} width={w - 1} height={h - 1} rx={4} fill={look.panel} stroke={look.line} />
      <g transform={`translate(${slot.x + 1} ${slot.y + 1})`}>
        <text x={8} y={headerH / 2} dominantBaseline="central" fontSize={11} fontWeight={500} fill={look.text}>
          {fit(node.name, 11, nameEnd - 8)}
        </text>
        {warnSev && <g transform={`translate(${badgeX} ${headerH / 2 - 5.5})`} color={look.sev[warnSev]}><WarnIcon size={11} /></g>}
        <text x={utilX} y={headerH / 2} dominantBaseline="central" textAnchor="end" fontSize={11}
          fill={tone ? look.sev[tone] : look.dim}>{utilText}</text>
        {laid.map((it, i) => it.empty ? (
          <g key={`empty-${i}`}>
            <rect x={it.x} y={it.y} width={Math.max(0, it.w - 2)} height={Math.max(0, it.h - 2)} rx={2}
              fill={`url(#hatch-${warnSev || "none"})`} stroke={look.line} />
            {it.w > 60 && it.h > 30 && (
              <text x={it.x + it.w / 2 - 1} y={it.y + it.h / 2 - 1} textAnchor="middle" dominantBaseline="central"
                fontSize={10} fill={hatchText}>idle · {Math.round((empty / cap) * 100)}%</text>
            )}
          </g>
        ) : (
          <SvgPod key={`pod-${i}`} it={it} metric={metric} look={look}
            matched={podMatched(it.pod)} dim={look.queryActive && !nodeDim && !podMatched(it.pod)} />
        ))}
      </g>
    </g>
  );
}

// PodBox's states: query dim wins over the workload highlight, which wins over a match.
function SvgPod({ it, metric, look, matched, dim }) {
  const { pod, x, y, w, h } = it;
  const role = look.role(podToken(pod, look.colorBy, look.nsMap));
  const neutral = look.role("--pod-neutral");
  const peer = !!look.highlight && workloadKey(pod.name) === look.highlight;
  const opacity = dim ? 0.16 : look.highlight && !peer ? (look.pinned ? 0.22 : 0.55) : 1;
  const ring = !dim && (matched || peer);
  const showName = w >= 52 && h >= 22;
  const headerH = showName ? 13 : 0;
  const containers = squarify(
    pod.containers.map(c => ({ container: c, value: metricValue(c, metric) })),
    2, headerH + 2, Math.max(0, w - 4), Math.max(0, h - headerH - 4)
  );
  const sev = w > 24 && h > 16 && worstFindingSeverity(pod.findings);
  return (
    <g opacity={opacity}>
      <rect x={x} y={y} width={Math.max(0, w - 2)} height={Math.max(0, h - 2)} rx={2}
        fill={tintHex(role, look.panel, look.tint.fill)}
        stroke={ring ? look.accent : tintHex(role, look.panel, look.tint.edge)} strokeWidth={ring ? 1.5 : 1} />
      {showName && (
        <text x={x + 5} y={y + 7} dominantBaseline="central" fontSize={9} fill={look.text}>{fit(pod.shortName, 9, w - 16)}</text>
      )}
      {sev && <g transform={`translate(${x + w - 14} ${y + 2})`} color={look.sev[sev]}><WarnIcon size={9} /></g>}
      {containers.map((c, i) => (
        <g key={i}>
          <rect x={x + c.x} y={y + c.y} width={Math.max(0, c.w - 1)} height={Math.max(0, c.h - 1)} rx={1}
            fill={tintHex(c.container.init ? neutral : role, look.panel, look.tint.box)} />
          {c.w > 40 && c.h > 18 && (
            <text x={x + c.x + c.w / 2} y={y + c.y + c.h / 2} textAnchor="middle" dominantBaseline="central"
              fontSize={9} fill={look.text}>{fit(c.container.name, 9, c.w - 6)}</text>
          )}
        </g>
      ))}
    </g>
  );
}


function treemapSVG(props) {
  const host = document.createElement("div");
  const root = ReactDOM.createRoot(host);
  ReactDOM.flushSync(() => root.render(<TreemapSVG {...props} />));
  const svg = new XMLSerializer().serializeToString(host.firstChild);
  root.unmount();
  return svg;
}

const svgURL = svg => "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);

// A data: URL, since the CSP's img-src allows data: but not blob:.
async function svgToPNG(svg, width, height) {
  const img = new Image();
  img.src = svgURL(svg);
  await img.decode();
  const c = document.createElement("canvas");
  c.width = width * SCALE;
  c.height = height * SCALE;
  c.getContext("2d").drawImage(img, 0, 0, c.width, c.height);
  return c;
}

const toBlob = canvas => new Promise(resolve => canvas.toBlob(resolve, "image/png"));

function download(blob, name) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// PDF through the browser's print dialog ("Save as PDF"): print CSS hides the
// app and shows only this image, so a live DOM re-flowing to paper size never
// comes into it. The 2D SVG stays vector in the PDF. Removed on afterprint,
// since print() does not block in every browser.
async function printImage(src) {
  const img = document.createElement("img");
  img.className = "print-sheet";
  img.src = src;
  await img.decode();
  document.body.append(img);
  addEventListener("afterprint", () => img.remove(), { once: true });
  print();
}

// k8sfoams-<context>-<view>-2026-09-28-1405
function fileName(context, view) {
  const d = new Date(), p = n => String(n).padStart(2, "0");
  const stamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}`;
  return ["k8sfoams", (context || "cluster").replace(/[^\w.-]+/g, "_"), view, stamp].filter(Boolean).join("-");
}

// A native <details> dropdown: no open state to keep, and Escape/outside
// clicks are not worth code here.
function ExportMenu({ view, context, treemap, gridRef, sceneRef }) {
  const ref = React.useRef(null);
  const q = context ? `?context=${encodeURIComponent(context)}` : "";
  const close = () => { ref.current.open = false; };
  const run = fn => () => { close(); fn(); };
  const svg2d = () => {
    const { width, height } = gridRef.current.getBoundingClientRect();
    const w = Math.round(width), h = Math.round(height);
    return { svg: treemapSVG({ ...treemap, width: w, height: h }), w, h };
  };
  const items = view === "3d" ? [
    // Without WebGL there is no scene to snapshot, only the notice.
    ["PNG image", run(async () => sceneRef.current &&
      download(await toBlob(sceneRef.current(SCALE)), `${fileName(context, "3d")}.png`))],
    ["PDF (print)", run(() => sceneRef.current && printImage(sceneRef.current(SCALE).toDataURL("image/png")))],
  ] : [
    ["SVG image", run(() => download(new Blob([svg2d().svg], { type: "image/svg+xml" }), `${fileName(context, "2d")}.svg`))],
    ["PNG image", run(async () => {
      const { svg, w, h } = svg2d();
      download(await toBlob(await svgToPNG(svg, w, h)), `${fileName(context, "2d")}.png`);
    })],
    ["PDF (print)", run(() => printImage(svgURL(svg2d().svg)))],
  ];
  return (
    <details className="export-menu" ref={ref}>
      <summary className="icon-btn" title="Export">
        <svg viewBox="0 0 16 16" width="14" height="14">
          <path d="M8 2 V10 M4.5 6.5 L8 10 L11.5 6.5 M3 13.5 H13" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </summary>
      <div className="export-pop">
        {items.map(([label, onClick]) => <button key={label} onClick={onClick}>{label}</button>)}
        <a href={`/report.csv${q}`} download={`${fileName(context, "")}.csv`} onClick={close}>CSV report</a>
        <a href={`/report.json${q}`} download={`${fileName(context, "")}.json`} onClick={close}>JSON report</a>
      </div>
    </details>
  );
}

window.k8sExport = { ExportMenu };
