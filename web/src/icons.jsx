// The few line icons the chrome uses, plus the severity glyph. 16×16 grid with
// currentColor strokes, so they follow the theme like text.

const ICON_PATHS = {
  logo: <><rect x="1.5" y="1.5" width="13" height="13" rx="2" /><path d="M7 1.5v13M7 8.5h7.5M11 8.5v6" /></>,
  cube: <path d="M8 1.8l5.5 3v6.4L8 14.2l-5.5-3V4.8zM2.5 4.8L8 7.8l5.5-3M8 7.8v6.4" />,
  alert: <path d="M8 2l6.5 11.5h-13zM8 6.5v3.2M8 11.6h.01" />,
  clock: <><circle cx="8" cy="8" r="6" /><path d="M8 4.5V8l2.5 1.5" /></>,
  flask: <path d="M6 2h4M6.5 2v4L2.8 12.6A1 1 0 0 0 3.7 14h8.6a1 1 0 0 0 .9-1.4L9.5 6V2" />,
  gear: <><circle cx="8" cy="8" r="2.2" /><path d="M8 1.8v2M8 12.2v2M1.8 8h2M12.2 8h2M3.6 3.6l1.4 1.4M11 11l1.4 1.4M3.6 12.4L5 11M11 5l1.4-1.4" /></>,
  chev: <path d="M4 6l4 4 4-4" />,
  refresh: <path d="M13 8a5 5 0 1 1-1.46-3.54M13 2.5V5h-2.5" />,
  max: <path d="M3 6V3h3M13 6V3h-3M3 10v3h3M13 10v3h-3" />,
  terminal: <path d="M2 3h12v10H2zM4.5 6.5l2 1.5-2 1.5M8 10h3.5" />,
  signout: <path d="M6 2.5H3.5v11H6M10 5l3 3-3 3M13 8H6.5" />,
  chat: <path d="M2.5 3h11v7.5H8L5 13v-2.5H2.5zM5 6.2h6M5 8.2h4" />,
  spark: <path d="M8 2v3M8 11v3M2 8h3M11 8h3M4 4l1.8 1.8M10.2 10.2L12 12M4 12l1.8-1.8M10.2 5.8L12 4" />,
  stop: <rect x="4" y="4" width="8" height="8" rx="1" />,
};

function Icon({ name, size = 16 }) {
  return (
    <svg viewBox="0 0 16 16" width={size} height={size} fill="none" stroke="currentColor" strokeWidth="1.4"
      strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{ICON_PATHS[name]}</svg>
  );
}

const GLYPH = { danger: "!", warn: "!", info: "i", ok: "✓" };

// Status is never colour alone: the glyph always sits next to a text label.
function SevGlyph({ sev, glyph }) {
  return <i className={`sev-glyph sev-${sev}`} aria-hidden="true">{glyph || GLYPH[sev] || "!"}</i>;
}

window.k8sIcons = { Icon, SevGlyph };
