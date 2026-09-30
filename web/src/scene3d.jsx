// scene3d.jsx — the 3D view on WebGL (three.js, bundled into three.js).
// Pods, plates and their overlays are a few InstancedMeshes, so the whole
// cluster is a handful of draw calls; the CSS 3D view this replaces built ~5
// DOM nodes per pod and fell to 1 fps at 5k pods. Frames are drawn on demand
// (camera move, data or style change, animation), never in an idle loop.
//
// Colours come from the CSS tokens (re-read on a theme change, so the scene
// follows the theme);
// the CSS filter values for dim / match / workload states are applied here in
// the same sRGB math.

const { workloadKey, podKey } = window.k8sWorkload;
const { findingInfo } = window.k8sPodAudit;
const { worstSeverity } = window.k8sNodeStatus;
const { podToken, utilTone, shade, hexRgb: hex, mixRgb: mix } = window.k8sPalette;
const { groupNodes, groupUsage } = window.k8sTopology;

const PLATE = 160;
const PLATE_GAP = 28;
const GROUP_GAP = 160;
// Floor frame around a group, beyond its outer plates.
const FRAME_PAD = 24;
const GROW_MS = 500;

const clamp = (lo, v, hi) => Math.max(lo, Math.min(hi, v));
// The CSS view's cubeDims(), in its 250px-plate pixels, scaled to this plate.
const CSS_PX = PLATE / 250;
const footprint = cpu => clamp(18, 10 + Math.sqrt(cpu) * 1.05, 48) * CSS_PX;
const tall = memMib => clamp(8, Math.sqrt(memMib) * 1.05, 78) * CSS_PX;
// rotateX(55deg) in the CSS scene is a 35° view elevation.
const CAMERA = [1, Math.tan((35 * Math.PI) / 180) * Math.SQRT2, 1];
const ease = t => 1 - Math.pow(1 - clamp(0, t, 1), 3);

// Palette tokens from styles.css, so the scene follows the stylesheet.
function token(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return v || fallback;
}

// CSS brightness() then saturate() (Filter Effects spec matrices), clamped
// after each step like the browser does.
function cssFilter([r, g, b], brightness, saturate) {
  [r, g, b] = [r, g, b].map(v => clamp(0, v * brightness, 1));
  const s = saturate;
  return [
    (0.213 + 0.787 * s) * r + (0.715 - 0.715 * s) * g + (0.072 - 0.072 * s) * b,
    (0.213 - 0.213 * s) * r + (0.715 + 0.285 * s) * g + (0.072 - 0.072 * s) * b,
    (0.213 - 0.213 * s) * r + (0.715 - 0.715 * s) * g + (0.072 + 0.928 * s) * b,
  ].map(v => clamp(0, v, 1));
}

// [brightness, saturate, opacity] per pod, mirroring the CSS cascade:
// query scope wins over workload state, and workload state wins over a match.
function podFilter(cube, look) {
  const outOfScope = look.plateDim(cube.node) || (look.queryActive && !look.matched(cube));
  if (outOfScope) return look.highlight ? [0.45, 0.3, 0.12] : [1, 1, 0.12];
  if (look.highlight) {
    const peer = cube.wl === look.highlight;
    if (look.pinned) return peer ? [1.4, 1.2, 1] : [0.45, 0.3, 0.38];
    return peer ? [1.2, 1, 1] : [0.7, 0.6, 0.7];
  }
  return look.queryActive ? [1.4, 1.3, 1] : [1, 1, 1];
}

// Side faces darken toward black, like the 2D map's borders darken its pods.
const FACE_SHADE = { top: 0, x: 0.16, z: 0.3 };

function webglAvailable() {
  try {
    const c = document.createElement("canvas");
    const gl = c.getContext("webgl2") || c.getContext("webgl");
    gl && gl.getExtension("WEBGL_lose_context")?.loseContext();
    return !!gl;
  } catch {
    return false;
  }
}

// Plates on a grid per group (topology.jsx), pods on a grid inside each
// plate, biggest footprint first. Pure: positions only, no three.js.
function layout(nodes, groupBy) {
  const plates = [], cubes = [], labels = [], frames = [];
  let x0 = 0, maxZ = 0;
  for (const { key, members } of groupNodes(nodes, groupBy)) {
    const cols = Math.ceil(Math.sqrt(members.length));
    members.forEach(({ node }, i) => {
      const x = x0 + (i % cols) * (PLATE + PLATE_GAP);
      const z = Math.floor(i / cols) * (PLATE + PLATE_GAP);
      maxZ = Math.max(maxZ, z);
      plates.push({ node, x, z });
      const pods = [...node.pods].sort((a, b) => b.cpu - a.cpu);
      const per = Math.max(1, Math.ceil(Math.sqrt(pods.length)));
      const cell = PLATE / per;
      pods.forEach((pod, j) => {
        const w = Math.min(cell * 0.86, footprint(pod.cpu));
        const h = tall(pod.mem);
        cubes.push({
          pod, node, w, h, wl: workloadKey(pod.name),
          x: x - PLATE / 2 + cell * ((j % per) + 0.5),
          z: z - PLATE / 2 + cell * (Math.floor(j / per) + 0.5),
          // A shell only grows on the axes that have a limit.
          shell: pod.cpuLimit != null || pod.memLimit != null,
          sw: pod.cpuLimit != null ? Math.min(cell * 0.98, footprint(pod.cpuLimit)) : w,
          sh: pod.memLimit != null ? tall(pod.memLimit) : h,
        });
      });
    });
    const blockW = Math.min(cols, members.length) * (PLATE + PLATE_GAP) - PLATE_GAP;
    const blockD = Math.floor((members.length - 1) / cols) * (PLATE + PLATE_GAP) + PLATE;
    if (key) {
      // Like a plate label, but for the group: its share of requested capacity
      // on the tighter resource, so an imbalanced zone or pool stands out.
      const u = groupUsage(members), n = members.length;
      labels.push({
        text: `${key} · ${n} node${n === 1 ? "" : "s"}`, util: Math.max(u.cpu, u.mem),
        x: x0 + blockW / 2 - PLATE / 2, z: -PLATE / 2 - 60,
      });
      frames.push({ x: x0 + blockW / 2 - PLATE / 2, z: blockD / 2 - PLATE / 2, w: blockW + FRAME_PAD * 2, d: blockD + FRAME_PAD * 2 });
    }
    x0 += cols * (PLATE + PLATE_GAP) + GROUP_GAP;
  }
  const width = Math.max(PLATE, x0 - GROUP_GAP - PLATE_GAP);
  return { plates, cubes, labels, frames, center: { x: width / 2 - PLATE / 2, z: maxZ / 2 }, size: Math.hypot(width, maxZ + PLATE) };
}

// Some faces of a unit box standing on y=0, as their own geometry, so each
// face kind gets exactly its CSS colour (BoxGeometry face order: +x -x +y -y +z -z).
function boxFaces(T, faces) {
  const src = new T.BoxGeometry(1, 1, 1).translate(0, 0.5, 0).toNonIndexed();
  const pos = src.getAttribute("position").array, out = [];
  for (const f of faces) out.push(...pos.slice(f * 18, f * 18 + 18));
  src.dispose();
  const g = new T.BufferGeometry();
  g.setAttribute("position", new T.Float32BufferAttribute(out, 3));
  return g;
}

function patternTexture(T, draw, repeat) {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  draw(c.getContext("2d"));
  const tex = new T.CanvasTexture(c);
  tex.wrapS = tex.wrapT = T.RepeatWrapping;
  tex.repeat.set(repeat, repeat);
  return tex;
}

// The plate label: a chip with the name in --text, utilisation in its tone, and a
// severity mark when unhealthy. Group labels use the same chip.
function labelSprite(T, { name, util, sev }, height, chip) {
  const px = 44, pad = 18, measure = document.createElement("canvas").getContext("2d");
  const font = weight => `${weight} ${px}px ${chip.font}`;
  const utilText = util == null ? "" : `${Math.round(util * 100)}%`;
  measure.font = font(400);
  const nameW = measure.measureText(name).width;
  measure.font = font(600);
  const utilW = measure.measureText(utilText).width;
  const markW = sev ? px * 0.6 : 0;
  const c = document.createElement("canvas");
  c.width = Math.ceil(pad + nameW + (utilText ? pad + utilW : 0) + (sev ? pad * 0.6 + markW : 0) + pad);
  c.height = px + 26;
  const g = c.getContext("2d"), mid = c.height / 2;
  g.fillStyle = chip.panel;
  g.fillRect(0, 0, c.width, c.height);
  g.strokeStyle = chip.line;
  g.lineWidth = 3;
  g.strokeRect(1.5, 1.5, c.width - 3, c.height - 3);
  let x = pad;
  g.textBaseline = "middle";
  g.font = font(400);
  g.fillStyle = chip.text;
  g.fillText(name, x, mid);
  x += nameW;
  if (utilText) {
    x += pad;
    g.font = font(600);
    const tone = utilTone(util);
    g.fillStyle = tone ? chip[tone] : chip.dim;
    g.fillText(utilText, x, mid);
    x += utilW;
  }
  if (sev) {
    x += pad * 0.6;
    g.fillStyle = chip[sev];
    g.beginPath();
    g.moveTo(x + markW / 2, mid - markW / 2);
    g.lineTo(x + markW, mid + markW / 2);
    g.lineTo(x, mid + markW / 2);
    g.fill();
  }
  const tex = new T.CanvasTexture(c);
  tex.colorSpace = T.SRGBColorSpace;
  const s = new T.Sprite(new T.SpriteMaterial({ map: tex, depthTest: false, transparent: true }));
  s.scale.set((c.width / c.height) * height, height, 1);
  s.renderOrder = 10;
  return s;
}

function Scene3D({
  nodes, match, zoom, groupBy, colorBy, nsMap, themeKey, memUnit, fmtMem, onFocus,
  highlight, highlightActive, onPodSelect, onPodHover, snapshotRef,
}) {
  const hostRef = React.useRef(null);
  const world = React.useRef(null);
  const [tip, setTip] = React.useState(null);
  const [ok] = React.useState(webglAvailable);

  // Handlers read the latest props without re-binding pointer listeners.
  const props = React.useRef({});
  props.current = { onFocus, onPodSelect, onPodHover };

  // One renderer, camera, controls and shared textures for the component's lifetime.
  React.useEffect(() => {
    if (!ok) return;
    const T = window.THREE, host = hostRef.current;
    const renderer = new T.WebGLRenderer({ antialias: true, alpha: true });
    renderer.setPixelRatio(window.devicePixelRatio);
    host.appendChild(renderer.domElement);
    const scene = new T.Scene();
    const root = new T.Group();
    scene.add(root);
    const cam = new T.OrthographicCamera(-1, 1, 1, -1, -20000, 20000);
    cam.position.set(...CAMERA).multiplyScalar(4000);
    const controls = new T.OrbitControls(cam, renderer.domElement);
    controls.maxPolarAngle = Math.PI * 0.47;

    // The CSS plate's 26px tech grid and its 6px-on/6px-off warning hatch,
    // scaled from the 250px CSS plate. The hatch is white, tinted per plate
    // with the severity colour; its two bands are .24 and .06 alpha.
    const grid = patternTexture(T, g => {
      g.fillStyle = "rgba(255,255,255,.6)";
      g.fillRect(0, 0, 64, 3);
      g.fillRect(0, 0, 3, 64);
    }, 250 / 26);
    const hatch = patternTexture(T, g => {
      g.fillStyle = "rgba(255,255,255,.06)";
      g.fillRect(0, 0, 64, 32);
      g.fillStyle = "rgba(255,255,255,.24)";
      g.fillRect(0, 32, 64, 32);
    }, 250 / 12);

    const w = {
      T, renderer, scene, root, cam, controls, grid, hatch, cubes: [], plates: [], labels: [], meshes: null,
      fit: 1, fittedFor: "", anim: null, tokens: new Map(),
    };
    // Tokens are read once per theme: the build effect clears this on a theme change.
    w.token = (name, fallback) => {
      if (!w.tokens.has(name)) w.tokens.set(name, token(name, fallback));
      return w.tokens.get(name);
    };
    let frame = 0;
    w.render = () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        if (w.anim && w.step(performance.now())) w.render();
        renderer.render(scene, cam);
      });
    };
    controls.addEventListener("change", w.render);

    const resize = () => {
      const { clientWidth: cw, clientHeight: ch } = host;
      renderer.setSize(cw, ch);
      Object.assign(cam, { left: -cw / 2, right: cw / 2, top: ch / 2, bottom: -ch / 2 });
      cam.updateProjectionMatrix();
      w.render();
    };
    const ro = new ResizeObserver(resize);
    ro.observe(host);

    // Picking: pod faces first, then plates. Ghosts (removed pods fading out)
    // are appended after the live pods and never picked.
    const ray = new T.Raycaster(), ptr = new T.Vector2();
    const pick = e => {
      if (!w.meshes) return null;
      const r = renderer.domElement.getBoundingClientRect();
      ptr.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
      ray.setFromCamera(ptr, cam);
      const { top, x, z, plates } = w.meshes;
      const hit = ray.intersectObjects([top, x, z, plates], false)[0];
      if (!hit) return null;
      if (hit.object === plates) return { node: w.plates[hit.instanceId].node };
      const cube = w.cubes[hit.instanceId];
      return cube && !cube.ghost ? { pod: cube.pod } : null;
    };
    let hoverFrame = 0, down = null;
    const onMove = e => {
      if (hoverFrame) return;
      hoverFrame = requestAnimationFrame(() => {
        hoverFrame = 0;
        const hit = pick(e);
        setTip(hit ? { ...hit, x: e.clientX, y: e.clientY } : null);
        props.current.onPodHover(hit && hit.pod ? workloadKey(hit.pod.name) : null);
      });
    };
    const onLeave = () => { setTip(null); props.current.onPodHover(null); };
    const onDown = e => { down = e.button === 0 ? { x: e.clientX, y: e.clientY } : null; };
    // A drag orbits the camera; only a still primary-button click selects.
    const onUp = e => {
      const was = down;
      down = null;
      if (e.button !== 0 || !was || Math.hypot(e.clientX - was.x, e.clientY - was.y) > 4) return;
      const hit = pick(e);
      if (hit && hit.pod) props.current.onPodSelect(workloadKey(hit.pod.name));
      else if (hit && hit.node) props.current.onFocus(hit.node);
    };
    const el = renderer.domElement;
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerleave", onLeave);
    el.addEventListener("pointerdown", onDown);
    el.addEventListener("pointerup", onUp);

    // For the PNG export. Without preserveDrawingBuffer the frame is only
    // readable in the task that drew it, so draw and copy it here, at `scale`
    // pixels per CSS pixel, onto the page background (the canvas is transparent).
    snapshotRef.current = scale => {
      const ratio = renderer.getPixelRatio();
      renderer.setPixelRatio(scale);
      renderer.render(scene, cam);
      const src = renderer.domElement, out = document.createElement("canvas");
      out.width = src.width;
      out.height = src.height;
      const g = out.getContext("2d");
      g.fillStyle = token("--bg", "#f2f3f5");
      g.fillRect(0, 0, out.width, out.height);
      g.drawImage(src, 0, 0);
      renderer.setPixelRatio(ratio);
      w.render();
      return out;
    };

    world.current = w;
    resize();
    return () => {
      snapshotRef.current = null;
      ro.disconnect();
      cancelAnimationFrame(frame);
      cancelAnimationFrame(hoverFrame);
      controls.dispose();
      w.dispose && w.dispose();
      grid.dispose();
      hatch.dispose();
      renderer.forceContextLoss();
      renderer.dispose();
      el.remove();
      world.current = null;
    };
  }, [ok]);

  // Rebuild geometry when the data or grouping changes. Pods that vanished
  // since the last build stay as shrinking ghosts; new ones grow in.
  React.useEffect(() => {
    const w = world.current;
    if (!w) return;
    const { T, root } = w;
    if (w.themeKey !== themeKey) { w.tokens.clear(); w.themeKey = themeKey; }
    const next = layout(nodes, groupBy);
    const before = new Map(w.cubes.filter(c => !c.ghost).map(c => [podKey(c.pod), c]));
    const now = new Set(next.cubes.map(c => podKey(c.pod)));
    const ghosts = [...before].filter(([k]) => !now.has(k)).map(([, c]) => ({ ...c, ghost: true }));
    const added = new Set(next.cubes.filter(c => !before.has(podKey(c.pod))));
    // ponytail: a context switch or first load replaces most pods; animating
    // all of them is noise, so animate only when under half changed.
    const animate = before.size > 0 && added.size < next.cubes.length / 2;
    const cubes = animate ? [...next.cubes, ...ghosts] : next.cubes;

    w.dispose && w.dispose();
    const n = Math.max(1, cubes.length), np = Math.max(1, next.plates.length);
    const unlit = () => new T.MeshBasicMaterial();
    const top = new T.InstancedMesh(boxFaces(T, [2]), unlit(), n);
    const x = new T.InstancedMesh(boxFaces(T, [0, 1]), unlit(), n);
    const z = new T.InstancedMesh(boxFaces(T, [4, 5]), unlit(), n);
    const shells = new T.InstancedMesh(new T.BoxGeometry(1, 1, 1).translate(0, 0.5, 0), new T.MeshBasicMaterial({
      color: w.token("--shell", "#1a1e25"), transparent: true, opacity: 0.12, depthWrite: false,
    }), n);
    const rims = new T.InstancedMesh(new T.BoxGeometry(1, 1, 1), unlit(), np);
    const plates = new T.InstancedMesh(new T.BoxGeometry(1, 1, 1), unlit(), np);
    const flat = () => new T.PlaneGeometry(1, 1).rotateX(-Math.PI / 2);
    const overlay = map => new T.MeshBasicMaterial({ map, transparent: true, depthWrite: false });
    const grids = new T.InstancedMesh(flat(), overlay(w.grid), np);
    const hatches = new T.InstancedMesh(flat(), overlay(w.hatch), np);
    for (const mesh of [top, x, z, shells]) mesh.count = cubes.length;
    for (const mesh of [rims, plates, grids, hatches]) mesh.count = next.plates.length;
    // Group frames: a --floor floor with a --line-2 rim, below the plates.
    const nf = Math.max(1, next.frames.length);
    const frameRims = new T.InstancedMesh(new T.BoxGeometry(1, 1, 1), new T.MeshBasicMaterial({ color: w.token("--line-2", "#c9ced7") }), nf);
    const frameFloors = new T.InstancedMesh(new T.BoxGeometry(1, 1, 1), new T.MeshBasicMaterial({ color: w.token("--floor", "#e4e6ea") }), nf);
    frameRims.count = frameFloors.count = next.frames.length;
    const m = new T.Matrix4();
    next.frames.forEach((f, i) => {
      m.makeScale(f.w + 6, 2, f.d + 6).setPosition(f.x, -4.2, f.z);
      frameRims.setMatrixAt(i, m);
      m.makeScale(f.w, 2, f.d).setPosition(f.x, -4, f.z);
      frameFloors.setMatrixAt(i, m);
    });
    next.plates.forEach((p, i) => {
      // The 1px CSS border as a slightly larger, lower slab around the plate.
      m.makeScale(PLATE + 2, 2, PLATE + 2).setPosition(p.x, -1.1, p.z);
      rims.setMatrixAt(i, m);
      m.makeScale(PLATE, 2, PLATE).setPosition(p.x, -1, p.z);
      plates.setMatrixAt(i, m);
    });
    root.add(frameRims, frameFloors, rims, plates, grids, hatches, z, x, top, shells);

    const chip = {
      font: w.token("--font-mono", "ui-monospace, monospace"),
      panel: w.token("--panel", "#ffffff"), line: w.token("--line-2", "#c9ced7"),
      text: w.token("--text", "#1a1e25"), dim: w.token("--text-dim", "#4b5362"),
      danger: w.token("--danger", "#ef4444"), warn: w.token("--warn", "#f59e0b"), info: w.token("--info", "#60a5fa"),
    };
    const labels = [];
    for (const p of next.plates) {
      const util = Math.max(p.node.cpuUsed / (p.node.cpuCapacity || 1), p.node.memUsed / (p.node.memCapacity || 1));
      const s = labelSprite(T, {
        name: p.node.name.replace(/\.ec2\.internal$/, "").toLowerCase(), util,
        sev: worstSeverity(p.node.warnings),
      }, 11, chip);
      s.position.set(p.x, 6, p.z - PLATE / 2 - 10);
      labels.push(s);
    }
    for (const g of next.labels) {
      const s = labelSprite(T, { name: g.text, util: g.util }, 26, chip);
      s.position.set(g.x, 10, g.z);
      labels.push(s);
    }
    root.add(...labels);
    root.position.set(-next.center.x, 0, -next.center.z);

    // scale 0..1 along height for growing/shrinking cubes.
    w.place = f => {
      cubes.forEach((c, i) => {
        const k = c.ghost ? 1 - f : animate && added.has(c) ? f : 1;
        m.makeScale(c.w, Math.max(0.001, c.h * k), c.w).setPosition(c.x, 0, c.z);
        top.setMatrixAt(i, m);
        x.setMatrixAt(i, m);
        z.setMatrixAt(i, m);
        const s = c.shell && k > 0 ? 1 : 0;
        m.makeScale(c.sw * 1.04 * s + 0.001, Math.max(0.001, c.sh * k * s), c.sw * 1.04 * s + 0.001).setPosition(c.x, 0, c.z);
        shells.setMatrixAt(i, m);
      });
      for (const mesh of [top, x, z, shells]) mesh.instanceMatrix.needsUpdate = true;
    };
    w.step = t => {
      const f = ease((t - w.anim) / GROW_MS);
      w.place(f);
      if (f >= 1) {
        w.anim = null;
        // Ghosts are appended after live pods; stop drawing and picking them.
        const live = next.cubes.length;
        for (const mesh of [top, x, z, shells]) {
          mesh.count = live;
          mesh.boundingSphere = mesh.boundingBox = null;
        }
      }
      return !!w.anim;
    };
    w.anim = animate ? performance.now() : null;
    w.place(animate ? 0 : 1);

    Object.assign(w, { cubes, plates: next.plates, labels, meshes: { top, x, z, shells, rims, plates, grids, hatches } });
    w.dispose = () => {
      const meshes = [frameRims, frameFloors, rims, plates, grids, hatches, z, x, top, shells];
      root.remove(...meshes, ...labels);
      for (const o of meshes) { o.geometry.dispose(); o.material.dispose(); o.dispose(); }
      for (const s of labels) { s.material.map.dispose(); s.material.dispose(); }
    };

    // Re-fit the camera only when the shape of the scene changed, so a
    // refresh never throws away the user's orbit.
    const shape = `${groupBy}:${nodes.length}`;
    if (shape !== w.fittedFor) {
      w.fittedFor = shape;
      const host = hostRef.current;
      w.fit = Math.min(host.clientWidth, host.clientHeight) / (next.size * 1.05);
      w.controls.target.set(0, 0, 0);
      w.cam.position.set(...CAMERA).multiplyScalar(4000);
      w.cam.zoom = w.fit * zoom;
      w.cam.updateProjectionMatrix();
      w.controls.update();
    }
    w.recolor && w.recolor();
    w.render();
  }, [nodes, groupBy, themeKey]);

  // Colour-only updates (query, workload highlight, node colours) never
  // touch geometry.
  React.useEffect(() => {
    const w = world.current;
    if (!w) return;
    const queryActive = !!(match && match.active);
    const look = {
      highlight, queryActive, pinned: highlightActive,
      matched: c => match.pods.has(c.pod),
      plateDim: n => queryActive && match.dimNodes.has(n.name),
    };
    const sevColor = {
      danger: hex(w.token("--danger", "#ef4444")), warn: hex(w.token("--warn", "#f59e0b")), info: hex(w.token("--info", "#60a5fa")),
    };
    w.recolor = () => {
      if (!w.meshes) return;
      const { T } = w, c = new T.Color(), m = new T.Matrix4();
      const set = (mesh, i, rgb) => mesh.setColorAt(i, c.setRGB(rgb[0], rgb[1], rgb[2], T.SRGBColorSpace));
      const plate = hex(w.token("--plate", "#ffffff")), edge = hex(w.token("--line-2", "#c9ced7"));
      const gridLine = hex(w.token("--line", "#dde0e6"));
      const ringRim = hex(w.token("--accent", "#1f5fd6"));
      // A pod's faces depend only on its colour role and one of six filter
      // states, so compute each combination once, not once per pod.
      const faces = new Map();
      w.cubes.forEach((cube, i) => {
        const role = podToken(cube.pod, colorBy, nsMap);
        const f = podFilter(cube, look), key = `${role}|${f}`;
        if (!faces.has(key)) {
          const base = hex(w.token(role, "#9aa0a9"));
          faces.set(key, ["top", "x", "z"].map(face => mix(plate, cssFilter(shade(base, FACE_SHADE[face]), f[0], f[1]), f[2])));
        }
        const [t, fx, fz] = faces.get(key);
        set(w.meshes.top, i, t);
        set(w.meshes.x, i, fx);
        set(w.meshes.z, i, fz);
      });
      w.plates.forEach((p, i) => {
        const dim = look.plateDim(p.node), sev = worstSeverity(p.node.warnings);
        set(w.meshes.plates, i, plate);
        set(w.meshes.rims, i, match && match.ring === p.node.name ? ringRim : edge);
        // A plate the query ruled out drops its inlay, warning hatch included.
        m.makeScale(dim || sev ? 0 : PLATE, 1, dim || sev ? 0 : PLATE).setPosition(p.x, 0.05, p.z);
        w.meshes.grids.setMatrixAt(i, m);
        set(w.meshes.grids, i, gridLine);
        m.makeScale(dim || !sev ? 0 : PLATE, 1, dim || !sev ? 0 : PLATE).setPosition(p.x, 0.1, p.z);
        w.meshes.hatches.setMatrixAt(i, m);
        set(w.meshes.hatches, i, sev ? sevColor[sev] : [0, 0, 0]);
        w.labels[i].material.opacity = dim ? 0.38 : 1;
      });
      for (const mesh of Object.values(w.meshes)) if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
      w.meshes.grids.instanceMatrix.needsUpdate = w.meshes.hatches.instanceMatrix.needsUpdate = true;
      w.meshes.grids.boundingSphere = w.meshes.grids.boundingBox = null;
      w.meshes.hatches.boundingSphere = w.meshes.hatches.boundingBox = null;
    };
    w.recolor();
    w.render();
  }, [nodes, groupBy, match, highlight, highlightActive, colorBy, nsMap, themeKey]);

  React.useEffect(() => {
    const w = world.current;
    if (!w) return;
    w.cam.zoom = w.fit * zoom;
    w.cam.updateProjectionMatrix();
    w.render();
  }, [zoom]);

  if (!ok) {
    return (
      <div className="scene-3d scene-nogl">
        <p>The 3D view needs WebGL, which this browser has turned off or does not support. The 2D map shows the same data.</p>
      </div>
    );
  }
  return (
    <div className="scene-3d">
      <div className="scene-gl" ref={hostRef} />
      <SceneLegend />
      <SceneTooltip tip={tip} memUnit={memUnit} fmtMem={fmtMem} />
    </div>
  );
}

function SceneLegend() {
  return (
    <div className="scene-legend">
      <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" aria-hidden="true">
        <polygon points="12,3 20,7.5 12,12 4,7.5" fill="var(--accent)" fillOpacity=".5" />
        <polygon points="4,7.5 12,12 12,21 4,16.5" />
        <polygon points="20,7.5 12,12 12,21 20,16.5" />
      </svg>
      <div>Footprint = CPU request</div>
      <div>Height = memory request</div>
      <div>Outline = limits, where set</div>
      <div>Striped plate = refuses new pods</div>
    </div>
  );
}

function SceneTooltip({ tip, memUnit, fmtMem }) {
  if (!tip) return null;
  const style = { left: tip.x + 14, top: tip.y + 14 };
  if (tip.node) {
    const n = tip.node;
    return (
      <div className="cube-tip" style={style}>
        <div className="cube-tip-name">{n.name}</div>
        <div className="cube-tip-row"><span>{(n.cpuUsed / 1000).toFixed(1)} / {(n.cpuCapacity / 1000).toFixed(0)}</span> cores</div>
        <div className="cube-tip-row"><span>{fmtMem(n.memUsed, memUnit)} / {fmtMem(n.memCapacity, memUnit, true)}</span> {memUnit}</div>
        <div className="cube-tip-foot">{[n.zone, n.pool, n.instanceType].filter(Boolean).join(" · ") || `${n.pods.length} pods`}</div>
      </div>
    );
  }
  const p = tip.pod;
  return (
    <div className="cube-tip" style={style}>
      <div className="cube-tip-name">{p.name}</div>
      <div className="cube-tip-row">
        <span>{(p.cpu / 1000).toFixed(2)}</span> cores
        {p.cpuLimit != null && <> · limit {(p.cpuLimit / 1000).toFixed(2)}</>}
      </div>
      <div className="cube-tip-row">
        <span>{fmtMem(p.mem, memUnit)}</span> {memUnit}
        {p.memLimit != null && <> · limit {fmtMem(p.memLimit, memUnit)}</>}
      </div>
      <div className="cube-tip-foot">
        {p.namespace} · {p.containers.length} container{p.containers.length === 1 ? "" : "s"}{p.qos && ` · ${p.qos}`}
      </div>
      {p.findings.length > 0 && (
        <div className="cube-tip-audit">
          {p.findings.map(f => <div key={f} className={`sev-${findingInfo(f).sev}`}>{findingInfo(f).label}</div>)}
        </div>
      )}
    </div>
  );
}

window.k8sScene3D = { Scene3D, layout, token };
