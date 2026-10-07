// Text formatting shared by the app, the panel and the chrome.

// Format a MiB memory value into the active unit. MiB/GiB keep their original
// precision (and integer capacity); TiB uses adaptive decimals so a non-zero
// quantity never renders as a flat "0" (TiB is coarse for node/pod memory).
function fmtMem(mib, unit, capacity = false) {
  const div = unit === "TiB" ? 1024 * 1024 : unit === "GiB" ? 1024 : 1;
  const v = mib / div;
  if (unit === "MiB") return v.toFixed(0);
  if (unit === "GiB") return v.toFixed(capacity ? 0 : 1);
  // TiB: grow decimals (2 → max 6) until the rounded value is non-zero.
  if (v === 0) return "0";
  let d = 2;
  while (d < 6 && Number(v.toFixed(d)) === 0) d++;
  return v.toFixed(d);
}

// Costs arrive in USD per hour and read as a month, like the bill: 730
// hours, OpenCost's month. The ~ marks every figure as an estimate.
function fmtCost(hourly) {
  const m = hourly * 730;
  return `~$${m >= 10 ? Math.round(m).toLocaleString("en-US") : m.toFixed(2)}/mo`;
}

function shortContext(ctx) {
  const last = ctx.split("/").pop();
  const region = ctx.match(/(us|eu|ap)-[a-z]+-\d+/);
  return region ? `${last} · ${region[0]}` : last;
}

function clock(ts) {
  return new Date(ts).toLocaleTimeString([], { hour12: false });
}

function timeAgo(ts) {
  const s = Math.floor((Date.now() - ts) / 1000);
  if (s < 5) return "just now";
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  return `${Math.floor(s / 3600)}h ago`;
}

window.k8sFormat = { fmtMem, fmtCost, shortContext, clock, timeAgo };
