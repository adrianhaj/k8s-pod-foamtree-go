// Per-viewer preferences kept in localStorage: theme now, panel state later.
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

// "system" follows the OS; the explicit choices beat it both ways.
function resolveTheme(pref, systemDark) {
  return pref === "light" || pref === "dark" ? pref : systemDark ? "dark" : "light";
}

// "system" sets no attribute, so CSS alone follows prefers-color-scheme and
// the first paint never flashes.
function applyThemePref(pref) {
  const root = document.documentElement;
  if (pref === "light" || pref === "dark") root.dataset.theme = pref;
  else delete root.dataset.theme;
}

const darkQuery = () => matchMedia("(prefers-color-scheme: dark)");

function currentTheme() {
  return resolveTheme(document.documentElement.dataset.theme || "system", darkQuery().matches);
}

// Anything that caches colours (the 3D scene) re-reads tokens on this signal.
function watchTheme(cb) {
  const mq = darkQuery();
  const mo = new MutationObserver(cb);
  mq.addEventListener("change", cb);
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => { mq.removeEventListener("change", cb); mo.disconnect(); };
}

window.k8sPrefs = { THEME_PREFS, safeStorage, readPref, writePref, resolveTheme, applyThemePref, currentTheme, watchTheme };
