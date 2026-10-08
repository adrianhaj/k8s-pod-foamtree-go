// Colour roles. Every colour is a CSS custom property from styles.css, so the
// 2D map, the 3D scene and exports follow the active theme. This file decides
// which property a pod takes, plus the sRGB maths the canvas and SVG paths need
// because they cannot evaluate color-mix().

const NS_SLOTS = 5; // --ns-0 … --ns-4; every other namespace is --ns-other
const QOS_ROLE = { BestEffort: "--danger", Burstable: "--warn", Guaranteed: "--ok" };
const COLOR_MODES = [
  { id: "namespace", label: "Namespace" },
  { id: "qos", label: "QoS" },
  { id: "problems", label: "Problems" },
  { id: "usage", label: "Usage" },
];
const byName = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

// The biggest namespaces by requested CPU get a slot. Slots stick: a namespace
// keeps its slot while it exists and only a freed slot goes to a newcomer, so a
// refresh never repaints the map.
function assignNamespaces(nodes, prev) {
  const cpu = new Map();
  for (const n of nodes) for (const p of n.pods) cpu.set(p.namespace, (cpu.get(p.namespace) || 0) + p.cpu);
  const out = new Map();
  for (const [ns, slot] of prev || []) if (cpu.has(ns)) out.set(ns, slot);
  const taken = new Set(out.values());
  const free = [];
  for (let s = 0; s < NS_SLOTS; s++) if (!taken.has(s)) free.push(s);
  const ranked = [...cpu.keys()].filter(ns => !out.has(ns)).sort((a, b) => cpu.get(b) - cpu.get(a) || byName(a, b));
  for (const ns of ranked) {
    if (free.length === 0) break;
    out.set(ns, free.shift());
  }
  return out;
}

// Share of the CPU request in use (pod.share, set by withUsage). Over the request
// is danger; under 30% is warn, because that is paying for slack. An HPA-scaled
// pod is never warn: its autoscaler adds replicas before usage nears the request.
const USAGE_LOW = 0.3;
function usageRole(share, hpa) {
  if (share == null) return "--pod-neutral";
  return share > 1 ? "--danger" : share < USAGE_LOW && !hpa ? "--warn" : "--ok";
}

function podToken(pod, colorBy, nsMap) {
  if (colorBy === "qos") return QOS_ROLE[pod.qos] || "--pod-neutral";
  if (colorBy === "usage") return usageRole(pod.share, pod.hpa);
  if (colorBy === "problems") {
    const sev = window.k8sPodAudit.worstFindingSeverity(pod.findings);
    return sev ? `--${sev}` : "--pod-neutral";
  }
  return nsMap.has(pod.namespace) ? `--ns-${nsMap.get(pod.namespace)}` : "--ns-other";
}

// Neutral until a node is nearly full: the old four-colour ramp coloured
// every node, including the ones that need nothing.
function utilTone(u) {
  return u > 0.95 ? "danger" : u > 0.85 ? "warn" : null;
}

const hexRgb = h => {
  const n = parseInt(h.replace("#", ""), 16);
  return [(n >> 16 & 255) / 255, (n >> 8 & 255) / 255, (n & 255) / 255];
};
const rgbHex = rgb => "#" + rgb.map(v => Math.round(Math.min(1, Math.max(0, v)) * 255).toString(16).padStart(2, "0")).join("");
const mixRgb = (under, over, t) => under.map((u, i) => u + (over[i] - u) * t);
const shade = (rgb, k) => mixRgb(rgb, [0, 0, 0], k);
// color-mix(in srgb, role pct, surface), as hex for the SVG export.
const tintHex = (roleHex, surfaceHex, pct) => rgbHex(mixRgb(hexRgb(surfaceHex), hexRgb(roleHex), pct));

window.k8sPalette = { QOS_ROLE, COLOR_MODES, assignNamespaces, podToken, USAGE_LOW, utilTone, hexRgb, mixRgb, shade, tintHex };
