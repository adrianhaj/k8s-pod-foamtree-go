// Per-viewer preferences kept in localStorage: theme and panel state.
// Storage can be missing or throw (private windows, blocked site data), so
// every read falls back and every write is best effort.

const THEME_PREFS = ["system", "light", "dark"];

function safeStorage() {
  try { return window.localStorage; } catch (e) { return null; }
}

function readPref(storage, key, fallback, valid) {
  try {
    const raw = storage ? storage.getItem(key) : null;
    if (raw == null) return fallback;
    const v = JSON.parse(raw);
    return valid(v) ? v : fallback;
  } catch (e) {
    return fallback;
  }
}

function writePref(storage, key, value) {
  try { if (storage) storage.setItem(key, JSON.stringify(value)); } catch (e) { /* best effort */ }
}

// "system" sets no attribute, so CSS alone follows prefers-color-scheme and
// the first paint never flashes.
function applyThemePref(pref) {
  const root = document.documentElement;
  if (pref === "light" || pref === "dark") root.dataset.theme = pref;
  else delete root.dataset.theme;
}

// Anything that caches colours (the 3D scene) re-reads tokens on this signal.
function watchTheme(cb) {
  const mq = matchMedia("(prefers-color-scheme: dark)");
  const mo = new MutationObserver(cb);
  mq.addEventListener("change", cb);
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => { mq.removeEventListener("change", cb); mo.disconnect(); };
}

const PANEL_KEY = "k8sfoams.panel";
const PANEL_TABS = ["problems", "changes", "drain"];
const PANEL_DEFAULT = { open: true, tab: "problems", max: false };
const validPanel = v => !!v && typeof v.open === "boolean" && typeof v.max === "boolean" && PANEL_TABS.includes(v.tab);

window.k8sPrefs = { PANEL_KEY, PANEL_DEFAULT, validPanel, THEME_PREFS, safeStorage, readPref, writePref, applyThemePref, watchTheme };
