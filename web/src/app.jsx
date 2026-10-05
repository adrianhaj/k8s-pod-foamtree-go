// Main app — console chrome + treemap grid for the k8sfoams dashboard.

const { useState, useEffect, useMemo, useRef } = React;
const { NodeCard, metricCap, metricValue } = window.k8sTreemap;
const { Scene3D } = window.k8sScene3D;
const { workloadKey, podKey } = window.k8sWorkload;
const { Icon } = window.k8sIcons;
const { LogsTab, selFor } = window.k8sLogs;
const { warnInfo, statusOf } = window.k8sNodeStatus;
const { FindingPill } = window.k8sPodAudit;
const { ExportMenu } = window.k8sExport;
const { getJSON, fitMatch, FitForm, FitSummary, FitVerdict, DrainResults } = window.k8sSimulate;
const { QOS_INFO, QOS_ORDER } = window.k8sQos;
const { assignNamespaces, utilTone, COLOR_MODES } = window.k8sPalette;
const { Legend } = window.k8sLegend;
const { groupNodes, GROUP_BY, podShape, stranded, largestFit } = window.k8sTopology;
const { pack, unpack, record, diff } = window.k8sHistory;
const { THEME_PREFS, safeStorage, readPref, writePref, applyThemePref, PANEL_KEY, PANEL_DEFAULT, validPanel, readViewParams, viewSearch } = window.k8sPrefs;
const { fmtMem, shortContext, clock } = window.k8sFormat;
const { buildProblems, problemChips } = window.k8sProblems;
const { pickShown } = window.k8sHighlight;
const { TAB_IDS, BottomPanel, ProblemsTab, PendingTab, ChangesTab, DrainTab, MapChips, showTab } = window.k8sPanel;
const { TopBar, SummaryStrip, Toolbar, Rail, SettingsMenu, attentionBySev } = window.k8sChrome;
const { useAssistant, AssistantTab } = window.k8sAssistant;
const THEME_KEY = "k8sfoams.theme";
const SIM_IDLE = { mode: "drain", node: null, result: null, error: null, busy: false };

const METRICS = [
  { id: "cpu", label: "CPU" },
  { id: "mem", label: "Memory" },
];

// Extended resources become metrics when a node offers them. Byte-sized ones
// are shown in the memory unit, the rest (GPUs…) are device counts.
const EXT_LABELS = { "nvidia.com/gpu": "GPU", "amd.com/gpu": "AMD GPU", "google.com/tpu": "TPU", "ephemeral-storage": "Storage" };
const isBytes = key => key === "ephemeral-storage" || key.startsWith("hugepages-");

function extMetric(key) {
  const label = EXT_LABELS[key] || (key.startsWith("hugepages-") ? `HugePages ${key.slice(10)}` : key.split("/").pop());
  return { id: key, label };
}

function fmtExt(v, key, memUnit, capacity = false) {
  return isBytes(key) ? fmtMem(v / (1024 * 1024), memUnit, capacity) : String(v);
}

const extUnit = (key, memUnit) => (isBytes(key) ? memUnit : extMetric(key).label);

// Backend memory weights are decimal kB (bitmath .kB, 1 kB = 1000 bytes),
// so MiB = kB * 1000 / 1024^2 — not a plain /1024, which would treat kB as KiB.
function kbToMib(kb) {
  return (kb * 1000) / (1024 * 1024);
}

// Merge separate CPU and Memory data structures from backend into rich unified structures.
function mergeResources(cpuData, memData) {
  const cpuGroups = cpuData.groups || [];
  const memGroups = memData.groups || [];

  const memNodesMap = new Map();
  for (const mg of memGroups) {
    memNodesMap.set(mg.label, mg);
  }

  return cpuGroups.map((cg, idx) => {
    const mg = memNodesMap.get(cg.label) || { weight: 0, groups: [] };

    // Group pods by label
    const cpuPods = cg.groups || [];
    const memPods = mg.groups || [];

    const memPodsMap = new Map();
    for (const mp of memPods) {
      memPodsMap.set(mp.label, mp);
    }

    const pods = [];
    let cpuUsed = 0;
    let memUsed = 0;

    for (const cp of cpuPods) {
      if (cp.label === 'empty') continue;

      const mp = memPodsMap.get(cp.label) || { weight: 0, groups: [] };

      // Group containers by label
      const cpuConts = cp.groups || [];
      const memConts = mp.groups || [];
      const memContsMap = new Map();
      for (const mc of memConts) {
        memContsMap.set(mc.label, mc);
      }

      const containers = cpuConts.map(cc => {
        const mc = memContsMap.get(cc.label) || { weight: 0 };
        return {
          name: cc.label,
          // Backend marks init containers with a grey color hint
          init: !!cc.color,
          cpu: cc.weight || 0,
          // Convert memory from kB to MiB
          mem: kbToMib(mc.weight || 0),
          ext: cc.extended || {}
        };
      });

      const podCpu = cp.weight || 0;
      const podMem = mp.weight || 0;

      cpuUsed += podCpu;
      memUsed += podMem;

      pods.push({
        name: cp.label,
        shortName: cp.label.split('-')[0],
        // Selector metadata for the query bar. Absent on an older backend, so
        // every field falls back to a value that simply never matches.
        namespace: cp.namespace || "",
        labels: cp.labels || {},
        qos: cp.qos || "",
        hasInit: !!cp.hasInitContainers,
        // Best-practice rule slugs, decided by the backend.
        findings: cp.findings || [],
        // Pending in-place resize; cpu/mem are what spec asks for.
        resize: cp.resize ? { message: cp.resize.message,
          cpu: cp.resize.desired, mem: mp.resize ? kbToMib(mp.resize.desired) : null } : null,
        // Container status: restarts, waiting reason, last exit. Absent on an older backend.
        phase: cp.phase || "",
        statuses: cp.statuses || [],
        // Effective request (what the scheduler reserves) — init containers
        // run sequentially, so this is max(sum regular, max init), not a sum.
        cpu: podCpu,
        // Convert memory from kB to MiB
        mem: kbToMib(podMem),
        // null = no ceiling on that axis.
        cpuLimit: cp.limit ?? null,
        memLimit: mp.limit != null ? kbToMib(mp.limit) : null,
        ext: cp.extended || {},
        containers
      });
    }

    // Convert node capacity from kB to MiB
    const memCapacity = kbToMib(mg.weight || 0);
    const convertedMemUsed = kbToMib(memUsed);

    return {
      id: `node-${idx}`,
      name: cg.label,
      region: cg.region || "",
      zone: cg.zone || "",
      pool: cg.pool || "",
      instanceType: cg.instanceType || "",
      capacityType: cg.capacityType || "",
      cpuCapacity: cg.weight || 0,
      memCapacity: memCapacity,
      cpuUsed,
      memUsed: convertedMemUsed,
      cpuFree: Math.max(0, (cg.weight || 0) - cpuUsed),
      memFree: Math.max(0, memCapacity - convertedMemUsed),
      // Extended capacity by resource name, in bytes or devices.
      ext: cg.extended || {},
      pods,
      // Node health from the backend. Absent on an older backend, so every
      // field falls back to what a plainly healthy node would report.
      warnings: cg.warnings || [],
      taints: cg.taints || [],
      conditions: cg.conditions || {},
      unschedulable: !!cg.unschedulable,
      status: statusOf(cg.warnings || [])
    };
  });
}

// An expired session answers API calls with 401: sign in again, then come back here.
function apiFetch(url) {
  return fetch(url).then(r => {
    if (r.status === 401) {
      window.location.assign('/auth/login?next=' + encodeURIComponent(window.location.pathname + window.location.search));
      throw new Error('Session expired, signing in again');
    }
    return r;
  });
}

// Bumps whenever the effective theme may have changed, so views that cache
// colours (the 3D scene) re-read the tokens.
function useThemeKey() {
  const [key, setKey] = useState(0);
  useEffect(() => window.k8sPrefs.watchTheme(() => setKey(k => k + 1)), []);
  return key;
}

function App() {
  const themeKey = useThemeKey();
  const [me, setMe] = useState(null);
  const [themePref, setThemePref] = useState(() => readPref(safeStorage(), THEME_KEY, "system", v => THEME_PREFS.includes(v)));
  useEffect(() => {
    applyThemePref(themePref);
    writePref(safeStorage(), THEME_KEY, themePref);
  }, [themePref]);
  const [panel, setPanel] = useState(() => readPref(safeStorage(), PANEL_KEY, window.innerHeight < 720 ? { ...PANEL_DEFAULT, open: false } : PANEL_DEFAULT, v => validPanel(v, TAB_IDS)));
  const panelSaved = useRef(false);
  useEffect(() => {
    if (!panelSaved.current) { panelSaved.current = true; return; }
    writePref(safeStorage(), PANEL_KEY, panel);
  }, [panel]);

  const [linked] = useState(() => readViewParams(window.location.search, {
    view: ["2d", "3d"],
    group: GROUP_BY.map(g => g.id),
    color: COLOR_MODES.map(c => c.id),
  }));
  const [view, setView] = useState(linked.view);
  const [zoom, setZoom] = useState(0.7);
  const [groupBy, setGroupBy] = useState(linked.group);
  const [metric, setMetric] = useState(linked.size);
  const [colorBy, setColorBy] = useState(linked.color);
  const [memUnit, setMemUnit] = useState("GiB");
  const [refreshInterval, setRefreshInterval] = useState(60);
  const [hintOpen, setHintOpen] = useState(false);
  const [contexts, setContexts] = useState([]);
  const [contextIdx, setContextIdx] = useState(0);
  const [query, setQuery] = useState(linked.q);
  const [refreshing, setRefreshing] = useState(false);
  const [lastRefresh, setLastRefresh] = useState(Date.now());
  const [focused, setFocused] = useState(null);
  // Cross-node workload highlighting. A click pins a workload; hover only
  // previews one, so a pinned selection always wins over the pointer.
  const [selectedWorkload, setSelectedWorkload] = useState(null);
  const [hoveredWorkload, setHoveredWorkload] = useState(null);
  const [nodes, setNodes] = useState([]);
  const [pending, setPending] = useState([]);
  const [logsPod, setLogsPod] = useState(null);
  const [logsText, setLogsText] = useState(null);
  const allPods = useMemo(() => nodes.flatMap(n => n.pods), [nodes]);
  const openLogs = pod => {
    setLogsPod(selFor(pod));
    setPanel(p => ({ ...p, open: true, tab: "logs" }));
    setFocused(null);
  };
  const [error, setError] = useState(null);
  const gridRef = useRef(null);
  const sceneRef = useRef(null);
  // Last "Can I fit this pod?" answer: one verdict per node, or null.
  const [fit, setFit] = useState(null);
  const [sim, setSim] = useState(SIM_IDLE);
  // ponytail: the drain answer is a snapshot; a refresh does not re-run it.
  const runDrain = async (name) => {
    if (!name) return;
    setSim(s => ({ ...s, mode: "drain", node: name, result: s.node === name ? s.result : null, busy: true, error: null }));
    try {
      const result = await getJSON(`/api/drain?${new URLSearchParams({ context, node: name })}`);
      setSim(s => (s.node === name ? { ...s, result, busy: false } : s));
    } catch (err) {
      setSim(s => (s.node === name ? { ...s, result: null, error: err.message, busy: false } : s));
    }
  };
  const podsByKey = useMemo(() => sim.result ? new Map(nodes.flatMap(n => n.pods.map(p => [podKey(p), p]))) : null, [nodes, sim.result]);
  const context = contexts[contextIdx] ? contexts[contextIdx].context : "";
  // Every refresh is recorded; `at` is the snapshot on screen (null = live).
  const [history, setHistory] = useState([]);
  const [at, setAt] = useState(null);
  const [playing, setPlaying] = useState(false);
  // Comparison baseline: a recorded snapshot, possibly of another context.
  const [base, setBase] = useState(null);
  const [baseNodes, setBaseNodes] = useState(null);
  const atRef = useRef(at);
  atRef.current = at;
  const lastRaw = useRef(new Map());

  useEffect(() => {
    apiFetch('/api/me').then(r => r.json()).then(setMe).catch(() => {});
  }, []);

  // Load contexts from server
  useEffect(() => {
    apiFetch('/contexts')
      .then(res => {
        if (!res.ok) throw new Error('API failed');
        return res.json();
      })
      .then(data => {
        if (data && data.length > 0) {
          setContexts(data);
          const linkedIdx = data.findIndex(c => c.context === linked.context);
          const activeIdx = data.findIndex(c => c.active);
          setContextIdx(linkedIdx !== -1 ? linkedIdx : activeIdx !== -1 ? activeIdx : 0);
        } else {
          console.warn("No contexts config found");
          setContexts([]);
          setContextIdx(0);
        }
      })
      .catch(err => {
        console.warn("Failed to fetch contexts:", err);
        setContexts([]);
        setContextIdx(0);
      });
  }, []);

  // Fetch cluster resource data
  const loadData = async () => {
    setRefreshing(true);
    try {
      const currentCtx = contexts[contextIdx];
      const ctxParam = currentCtx ? `?context=${encodeURIComponent(currentCtx.context)}` : '';
      const t = Date.now();

      const [cpuText, memText] = await Promise.all([
        apiFetch(`/resources/cpu${ctxParam}`).then(r => {
          if (!r.ok) throw new Error(`CPU resources endpoint returned status ${r.status}`);
          return r.text();
        }),
        apiFetch(`/resources/memory${ctxParam}`).then(r => {
          if (!r.ok) throw new Error(`Memory resources endpoint returned status ${r.status}`);
          return r.text();
        })
      ]);

      const raw = `[${cpuText},${memText}]`;
      const [cpuRes, memRes] = JSON.parse(raw);
      // While scrubbing, refreshes keep recording but leave the screen alone.
      if (atRef.current == null) { setNodes(mergeResources(cpuRes, memRes)); setPending(cpuRes.pending || []); }
      setError(null);
      setLastRefresh(Date.now());
      // An unchanged cluster answers byte for byte the same: nothing to record.
      const name = currentCtx ? currentCtx.context : "";
      if (lastRaw.current.get(name) !== raw) {
        lastRaw.current.set(name, raw);
        const entry = await pack(t, name, raw);
        setHistory(h => record(h, entry));
      }
    } catch (err) {
      console.error("Error loading resources from live cluster:", err);
      setError(err.message || String(err));
    } finally {
      setRefreshing(false);
    }
  };

  useEffect(() => {
    if (contexts.length > 0) {
      loadData();
    }
  }, [contextIdx, contexts.length]);

  // Switching context swaps the whole data set, so a workload pinned in the
  // previous cluster is meaningless in the new one: it would match nothing and
  // dim every pod on screen with no pod glowing to explain why.
  useEffect(() => {
    setSelectedWorkload(null);
    setHoveredWorkload(null);
    setFit(null);
    setSim(SIM_IDLE);
    setAt(null);
    setPlaying(false);
    nsRef.current = new Map();
  }, [contextIdx]);

  // The address bar is always a link to this view. replaceState, so Back
  // still leaves the page. A single context (in-cluster) needs no name.
  useEffect(() => {
    if (contexts.length === 0) return;
    const search = viewSearch({ context: contexts.length > 1 ? context : "", view, size: metric, group: groupBy, color: colorBy, q: query });
    if (search !== window.location.search) {
      try {
        window.history.replaceState(null, "", window.location.pathname + search + window.location.hash);
      } catch {}
    }
  }, [contexts, context, view, metric, groupBy, colorBy, query]);

  const ctxName = contexts[contextIdx] ? contexts[contextIdx].context : "";
  const entries = useMemo(() => history.filter(e => e.context === ctxName), [history, ctxName]);

  // Keep a stable ref to the latest loadData so the auto-refresh interval
  // always calls the current closure without re-subscribing each render.
  const loadDataRef = useRef(loadData);
  loadDataRef.current = loadData;

  // Scrubbing shows a recorded snapshot; going back to live fetches a fresh one.
  useEffect(() => {
    if (at == null) {
      if (contexts.length > 0) loadDataRef.current();
      return;
    }
    const entry = entries.find(e => e.t === at);
    if (!entry) { setAt(null); return; }
    let stale = false;
    unpack(entry).then(([cpu, mem]) => { if (!stale) { setNodes(mergeResources(cpu, mem)); setPending(cpu.pending || []); } });
    return () => { stale = true; };
  }, [at]);

  // Playback steps one snapshot a second and stops at live. From live it
  // starts at the oldest snapshot.
  useEffect(() => {
    if (!playing) return;
    const id = setTimeout(() => {
      const next = entries[entries.findIndex(e => e.t === at) + 1];
      if (!next || next === entries[entries.length - 1]) {
        setAt(null);
        setPlaying(false);
      } else setAt(next.t);
    }, 1000);
    return () => clearTimeout(id);
  }, [playing, at, entries]);

  useEffect(() => {
    setBaseNodes(null);
    if (!base) return;
    let stale = false;
    unpack(base).then(([cpu, mem]) => { if (!stale) setBaseNodes(mergeResources(cpu, mem)); });
    return () => { stale = true; };
  }, [base]);

  const changes = useMemo(() => (base && baseNodes ? diff(baseNodes, nodes) : null), [base, baseNodes, nodes]);
  const toggleCompare = () => setBase(b => (b ? null : at == null ? entries[entries.length - 1] : entries.find(e => e.t === at)));
  const goLive = () => { setPlaying(false); setAt(null); };

  const metrics = useMemo(() => {
    const keys = new Set(nodes.flatMap(n => Object.keys(n.ext)));
    return [...METRICS, ...[...keys].sort().map(extMetric)];
  }, [nodes]);
  // A context without the chosen resource shows CPU; switching back restores it.
  const activeMetric = metrics.some(m => m.id === metric) ? metric : "cpu";

  const parsedQuery = useMemo(() => window.k8sQuery.parseQuery(query), [query]);

  // Matched pods are highlighted and unmatched ones dimmed — nodes are never
  // removed. A malformed query stays inert (and reports itself in the header)
  // rather than dimming everything on a half-typed token.
  const match = useMemo(() => {
    const active = parsedQuery.terms.length > 0 && parsedQuery.errors.length === 0;
    const pods = new Set();
    const dimNodes = new Set();
    let total = 0;
    for (const n of nodes) {
      total += n.pods.length;
      if (!active) continue;
      if (!window.k8sQuery.nodeMatches(n, parsedQuery)) dimNodes.add(n.name);
      for (const p of n.pods) {
        if (window.k8sQuery.podMatches(p, parsedQuery, n)) pods.add(p);
      }
    }
    return { active, pods, dimNodes, count: active ? pods.size : total, total, errors: parsedQuery.errors };
  }, [nodes, parsedQuery]);

  const changeLists = useMemo(() => changes ? {
    added: changes.added.map(x => podKey(x.pod)),
    removed: changes.removed.map(x => podKey(x.pod)),
    resized: changes.resized.map(r => resizeText(r, memUnit)),
    nodes: changes.nodes.map(d => nodeDeltaText(d, memUnit)),
  } : null, [changes, memUnit]);

  // A fit verdict takes over the map's dimming until it is cleared.
  const fitShown = useMemo(() => (fit ? fitMatch(nodes, fit) : null), [fit, nodes]);
  const shown = useMemo(() => pickShown({
    match, tab: panel.open ? panel.tab : null, changes, nodes, logsPod,
    sim: { mode: sim.mode, fit: fitShown, node: sim.node },
  }), [match, panel.open, panel.tab, changes, nodes, fitShown, sim.mode, sim.node, logsPod]);

  const highlight = selectedWorkload || hoveredWorkload;
  const highlightActive = !!selectedWorkload;

  const toggleWorkload = (wl) => setSelectedWorkload(prev => (prev === wl ? null : wl));

  // Replica spread of the pinned workload. Counted over every node, ignoring
  // the query — the point of the readout is the cluster-wide picture.
  const workloadStats = useMemo(() => {
    if (!selectedWorkload) return null;
    const spread = new Set();
    let replicas = 0;
    for (const n of nodes) {
      for (const p of n.pods) {
        if (workloadKey(p.name) !== selectedWorkload) continue;
        replicas++;
        spread.add(n.name);
      }
    }
    return { key: selectedWorkload, replicas, nodes: spread.size };
  }, [nodes, selectedWorkload]);

  // Totals
  const totals = useMemo(() => {
    const t = { cpuCap: 0, cpuUsed: 0, memCap: 0, memUsed: 0, extCap: 0, extUsed: 0, pods: 0, nodes: nodes.length };
    for (const n of nodes) {
      t.cpuCap += n.cpuCapacity;
      t.cpuUsed += n.cpuUsed;
      t.memCap += n.memCapacity;
      t.memUsed += n.memUsed;
      t.pods += n.pods.length;
      t.extCap += n.ext[activeMetric] || 0;
      for (const p of n.pods) t.extUsed += p.ext[activeMetric] || 0;
    }
    return t;
  }, [nodes, activeMetric]);

  // Judged against the snapshot on screen, so history playback stays honest.
  const shape = useMemo(() => podShape(nodes), [nodes]);
  const largest = useMemo(() => largestFit(nodes, shape), [nodes, shape]);

  // Auto-refresh tick — re-fetch live cluster data every refreshInterval seconds.
  useEffect(() => {
    if (!refreshInterval) return;
    const id = setInterval(() => {
      if (contexts.length > 0) loadDataRef.current();
    }, refreshInterval * 1000);
    return () => clearInterval(id);
  }, [refreshInterval, contexts.length]);

  // React fires no mouseleave when the hovered pod unmounts — a refresh
  // dropping the pod or a view switch both do that — so a stale preview would
  // dim the grid with the cursor over nothing. Drop the preview whenever the
  // rendered set is replaced; the pin is unaffected.
  useEffect(() => { setHoveredWorkload(null); }, [nodes, view]);

  // Escape clears the highlight — pin and hover preview alike.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== "Escape") return;
      setSelectedWorkload(null);
      setHoveredWorkload(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const problems = useMemo(() => buildProblems(nodes), [nodes]);
  const chips = useMemo(() => problemChips(problems), [problems]);

  const assistant = useAssistant({
    context, nodes, totals, problems, focused, logsPod, logsText, setPanel, setFocused,
    open: panel.open && panel.tab === "assistant",
  });

  // Every class gets a row, even at zero — "no BestEffort pods" is the answer
  // an SRE is usually looking for. Pods with no reported class are not counted.
  const qosBreakdown = useMemo(() => {
    const counts = new Map(QOS_ORDER.map(q => [q, 0]));
    for (const n of nodes) {
      for (const p of n.pods) if (counts.has(p.qos)) counts.set(p.qos, counts.get(p.qos) + 1);
    }
    return QOS_ORDER.map(q => ({ qos: q, count: counts.get(q), ...QOS_INFO[q] }));
  }, [nodes]);

  // Namespace colour slots stick across refreshes; a context switch starts over.
  const nsRef = useRef(new Map());
  const nsMap = useMemo(() => (nsRef.current = assignNamespaces(nodes, nsRef.current)), [nodes]);
  const fmtReq = (v, m) => (m === "cpu" ? `${Math.round(v)}m` : m === "mem" ? `${fmtMem(v, memUnit)} ${memUnit}` : fmtExt(v, m, memUnit));

  const attention = useMemo(() => attentionBySev(nodes), [nodes]);
  const showGroupBy = useMemo(() => nodes.some(n => n.zone || n.region || n.pool || n.instanceType || n.capacityType), [nodes]);
  const ext = activeMetric !== "cpu" && activeMetric !== "mem" && metrics.find(m => m.id === activeMetric);
  const extCell = ext && {
    label: `${ext.label} requested`, u: totals.extUsed / (totals.extCap || 1),
    value: fmtExt(totals.extUsed, activeMetric, memUnit), of: `of ${fmtExt(totals.extCap, activeMetric, memUnit, true)} ${extUnit(activeMetric, memUnit)}`,
  };
  const openTab = id => setPanel(p => (p.open && p.tab === id ? { ...p, open: false } : showTab(p, id)));

  return (
    <div className="app">
      <TopBar contexts={contexts} contextIdx={contextIdx} setContextIdx={setContextIdx}
        queryBar={<QueryBar query={query} setQuery={setQuery} match={match} hintOpen={hintOpen} setHintOpen={setHintOpen} />}
        error={error} lastRefresh={lastRefresh} refreshInterval={refreshInterval} setRefreshInterval={setRefreshInterval}
        onRefresh={loadData} refreshing={refreshing} me={me}
        exportMenu={<ExportMenu view={view} context={context} gridRef={gridRef} sceneRef={sceneRef}
          treemap={{ nodes, metric: activeMetric, match: shown, highlight, highlightActive, colorBy, nsMap }} />} />
      <SummaryStrip totals={totals} memUnit={memUnit} qosBreakdown={qosBreakdown} attention={attention}
        extCell={extCell} query={query} setQuery={setQuery} largest={largest} />
      <Toolbar view={view} metric={activeMetric} metrics={metrics} setMetric={setMetric} zoom={zoom} setZoom={setZoom}
        groupBy={groupBy} setGroupBy={setGroupBy} showGroupBy={showGroupBy} colorBy={colorBy} setColorBy={setColorBy}
        legend={<Legend colorBy={colorBy} nsMap={nsMap} view={view} />} />

      <div className="grid-wrap has-rail" ref={gridRef}>
        {error && (
          <div className="error-banner">
            <span>Failed to connect to cluster: {error}</span>
          </div>
        )}
        <Rail view={view} setView={setView} panel={panel} openTab={openTab} findings={problems.length}
          settings={<SettingsMenu themePref={themePref} setThemePref={setThemePref} memUnit={memUnit} setMemUnit={setMemUnit} onAssistant={assistant.openConnection} />} />
        <MapChips lit={shown.lit} ring={shown.ring}
          litPod={logsPod ? podKey(logsPod) : ""}
          onLogs={() => { const p = allPods.find(x => workloadKey(x.name) === selectedWorkload); if (p) openLogs(p); }}
          workload={workloadStats} onClearWorkload={() => setSelectedWorkload(null)}
          at={at} onLive={goLive} />
        {view === "3d" ? (
          <Scene3D
            nodes={nodes}
            match={shown}
            zoom={zoom}
            groupBy={groupBy}
            nsMap={nsMap}
            themeKey={themeKey}
            colorBy={colorBy}
            memUnit={memUnit}
            fmtMem={fmtMem}
            onFocus={setFocused}
            highlight={highlight}
            highlightActive={highlightActive}
            onPodSelect={toggleWorkload}
            onPodHover={setHoveredWorkload}
            snapshotRef={sceneRef}
          />
        ) : (
          <TreemapGrid
            nodes={nodes}
            shape={shape}
            match={shown}
            metric={activeMetric}
            groupBy={groupBy}
            colorBy={colorBy}
            nsMap={nsMap}
            fmtReq={fmtReq}
            onFocus={setFocused}
            highlight={highlight}
            highlightActive={highlightActive}
            onPodSelect={toggleWorkload}
            onPodHover={setHoveredWorkload}
          />
        )}
      </div>

      <BottomPanel panel={panel} setPanel={setPanel} counts={{ problems: problems.length, pending: pending.length, changes: changes ? changes.added.length + changes.removed.length + changes.resized.length : 0 }}>
        {panel.tab === "problems" && (
          <ProblemsTab rows={problems} chips={chips} query={query} setQuery={setQuery}
            onPickNode={name => setFocused(nodes.find(n => n.name === name) || null)} />
        )}
        {panel.tab === "pending" && <PendingTab pods={pending} />}
        {panel.tab === "changes" && (
          <ChangesTab entries={entries} at={at} atLabel={at == null ? "Live" : clock(at)}
            onScrub={i => { setPlaying(false); setAt(i === entries.length - 1 ? null : entries[i].t); }}
            playing={playing} onPlay={() => setPlaying(p => !p)} onLive={goLive}
            base={base} onCompare={toggleCompare}
            baseLabel={base ? `${clock(base.t)}${base.context !== ctxName ? ` of ${shortContext(base.context)}` : ""}` : ""}
            lists={changeLists} />
        )}
        {panel.tab === "drain" && (
          <DrainTab mode={sim.mode} setMode={mode => setSim(s => ({ ...s, mode }))}
            node={sim.node} setNode={name => setSim(s => ({ ...SIM_IDLE, mode: s.mode, node: name }))}
            nodes={nodes} busy={sim.busy} error={sim.error} onRun={() => runDrain(sim.node)}
            drainBody={sim.result && <DrainResults result={sim.result} podsByKey={podsByKey} fmtReq={fmtReq} />}
            fitBody={<><FitForm context={context} onResult={setFit} />{fit && <FitSummary result={fit} onClear={() => setFit(null)} />}</>} />
        )}
        {panel.tab === "logs" && (
          <LogsTab context={context} sel={logsPod} setSel={setLogsPod} pods={allPods} onLoaded={setLogsText} onAnalyze={assistant.analyzePod} />
        )}
        {panel.tab === "assistant" && (
          <AssistantTab a={assistant} />
        )}
      </BottomPanel>

      {focused && (
        <FocusOverlay node={focused} onClose={() => setFocused(null)} metric={metric} memUnit={memUnit} onLogs={openLogs}
          strand={stranded(focused, shape)}
          fitReasons={fit && (fit.find(v => v.node === focused.name) || {}).reasons}
          onDrain={name => { setPanel(p => ({ ...p, open: true, tab: "drain" })); setFocused(null); runDrain(name); }} />
      )}
    </div>
  );
}

function resizeText(r, memUnit) {
  const list = set => [...set].sort((a, b) => a - b);
  const axis = (a, b, f, unit) => {
    const from = list(a).map(f).join(","), to = list(b).map(f).join(",");
    return from === to ? "" : ` · ${from} → ${to} ${unit}`;
  };
  return r.key + axis(r.before.cpu, r.after.cpu, v => (v / 1000).toFixed(2), "cores")
    + axis(r.before.mem, r.after.mem, v => fmtMem(v, memUnit), memUnit);
}

function nodeDeltaText(d, memUnit) {
  const signed = s => (s.startsWith("-") ? s : `+${s}`);
  const state = !d.before ? " (new)" : !d.after ? " (gone)" : "";
  return `${d.name}${state} · ${signed(String(d.pods))} pods · ${signed((d.cpu / 1000).toFixed(2))} cores · ${signed(fmtMem(d.mem, memUnit))} ${memUnit}`;
}

// Query bar — search input plus live match count, inline token errors and a
// hint listing the grammar. The grammar itself lives in query.jsx.
function QueryBar({ query, setQuery, match, hintOpen, setHintOpen }) {
  const inputRef = useRef(null);
  useEffect(() => {
    const onKey = e => {
      if (e.key !== "/" || e.target.closest("input, textarea, select")) return;
      e.preventDefault();
      inputRef.current && inputRef.current.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const errors = (match && match.errors) || [];
  const invalid = errors.length > 0;
  const counting = !!query && !invalid && match;

  return (
    <div className="query-bar">
      <div className={`search ${invalid ? "search-invalid" : ""}`}>
        <svg viewBox="0 0 16 16" width="14" height="14"><circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.5" fill="none" /><path d="M11 11l3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg>
        <input id="query" ref={inputRef} value={query} onChange={e => setQuery(e.target.value)}
          onFocus={() => setHintOpen(true)} onBlur={() => setHintOpen(false)}
          spellCheck="false"
          placeholder="Filter pods, e.g. ns:payments qos:Burstable app=api" />
        {counting && (
          <span className={`query-count ${match.count === 0 ? "query-count-none" : ""}`}>
            {match.count} / {match.total} pods
          </span>
        )}
        {query && <button className="search-clear" onClick={() => setQuery("")}>×</button>}
      </div>

      {invalid && (
        <div className="query-pop query-errors">
          {errors.map((e, i) => (
            <div key={i} className="query-err"><code>{e.token}</code><span>{e.message}</span></div>
          ))}
        </div>
      )}

      {hintOpen && !invalid && (
        <div className="query-pop query-hint">
          <div className="query-hint-title">Filter tokens · combined with AND</div>
          {window.k8sQuery.TOKEN_HINTS.map((h, i) => (
            <div key={i} className="query-hint-row"><code>{h.form}</code><span>{h.desc}</span></div>
          ))}
          <div className="query-hint-foot">Quote values with spaces: app="my app"</div>
        </div>
      )}
    </div>
  );
}

/* ─────────── Grid ─────────── */

// Height of a group's label strip in the 2D map.
const GROUP_HEAD = 24;

function TreemapGrid({
  nodes, shape, match, metric, groupBy, colorBy, nsMap, fmtReq, onFocus,
  highlight, highlightActive, onPodSelect, onPodHover,
}) {
  const containerRef = useRef(null);
  const [box, setBox] = useState({ w: 0, h: 0 });

  useEffect(() => {
    if (!containerRef.current) return;
    const measure = () => {
      const r = containerRef.current.getBoundingClientRect();
      setBox({ w: r.width, h: r.height });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(containerRef.current);
    return () => ro.disconnect();
  }, []);

  const cap = n => metricCap(n, metric);
  const used = n => (metric === "cpu" ? n.cpuUsed : metric === "mem" ? n.memUsed
    : n.pods.reduce((s, p) => s + metricValue(p, metric), 0));
  const ready = box.w > 0 && box.h > 0;

  // Squarify of nodes themselves, sized by capacity, into one rect.
  const cards = (members, x, y, w, h) => window.k8sTreemap.squarify(
    members.map(m => ({ ...m, value: cap(m.node) })), x, y, w, h
  ).map(it => {
    return (
      <div key={it.node.id} className="grid-slot"
        style={{
          left: it.x, top: it.y, width: it.w - 6, height: it.h - 6,
        }}>
        <NodeCard
          node={it.node}
          match={match}
          metric={metric}
          colorBy={colorBy}
          nsMap={nsMap}
          fmtReq={fmtReq}
          onClick={() => onFocus(it.node)}
          highlight={highlight}
          highlightActive={highlightActive}
          onPodSelect={onPodSelect}
          onPodHover={onPodHover}
        />
      </div>
    );
  });

  // Grouped, the groups are squarified by total capacity first, then each
  // group's nodes inside its frame, under a label with its utilisation.
  const groups = !ready || groupBy === "none" ? [] : window.k8sTreemap.squarify(
    groupNodes(nodes, groupBy).map(g => ({ ...g, value: g.members.reduce((s, m) => s + cap(m.node), 0) })),
    0, 0, box.w, box.h
  );

  return (
    <div className="grid" ref={containerRef}>
      {ready && groupBy === "none" && cards(nodes.map(node => ({ node })), 0, 0, box.w, box.h)}
      {groups.map(g => {
        const u = g.members.reduce((s, m) => s + used(m.node), 0) / (g.value || 1), n = g.members.length;
        const s = g.members.reduce((t, m) => {
          const x = stranded(m.node, shape);
          return { cpu: t.cpu + x.cpu, mem: t.mem + x.mem };
        }, { cpu: 0, mem: 0 });
        return (
          <div key={g.key} className="group-box" style={{ left: g.x, top: g.y, width: g.w - 8, height: g.h - 8 }}>
            <div className="group-label" style={{ height: GROUP_HEAD }}>
              <span className="group-name">{g.key}</span>
              <span className="group-meta">{n} node{n === 1 ? "" : "s"}</span>
              {s.cpu >= 1 && <span className="group-meta" title="Free CPU with no memory to pair at the median pod shape">{fmtReq(s.cpu, "cpu")} stranded</span>}
              {parseFloat(fmtReq(s.mem, "mem")) > 0 && <span className="group-meta" title="Free memory with no CPU to pair at the median pod shape">{fmtReq(s.mem, "mem")} stranded</span>}
              <span className={`group-util${utilTone(u) ? ` tone-${utilTone(u)}` : ""}`}>{Math.round(u * 100)}%</span>
            </div>
            {cards(g.members, 6, GROUP_HEAD, g.w - 16, g.h - GROUP_HEAD - 10)}
          </div>
        );
      })}
    </div>
  );
}

/* ─────────── Focus overlay ─────────── */

function FocusOverlay({ node, onClose, metric, memUnit, fitReasons, onDrain, strand, onLogs }) {
  // At most one axis is stranded; hide figures that would print as zero.
  const strandNote = strand.cpu >= 5 ? `${(strand.cpu / 1000).toFixed(2)} cores free with no memory to pair`
    : Number(fmtMem(strand.mem, memUnit)) > 0 ? `${fmtMem(strand.mem, memUnit)} ${memUnit} free with no CPU to pair`
    : null;
  return (
    <div className="overlay" onClick={onClose}>
      <div className="overlay-card" onClick={e => e.stopPropagation()}>
        <div className="overlay-head">
          <div>
            <div className="overlay-title">{node.name}</div>
            <div className="overlay-sub">
              {[node.instanceType, node.zone || node.region, node.pool].filter(Boolean).map(s => `${s} · `)}
              <span className={`status-pill status-${node.status}`}>{node.status}</span>
            </div>
          </div>
          <button className="icon-btn" onClick={onClose}>
            <svg viewBox="0 0 16 16" width="14" height="14"><path d="M4 4 L12 12 M12 4 L4 12" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg>
          </button>
        </div>
        <div className="overlay-stats">
          <div className="ov-stat">
            <div className="ov-label">CPU</div>
            <div className="ov-val">{(node.cpuUsed / 1000).toFixed(2)} / {(node.cpuCapacity / 1000).toFixed(0)} <span>cores</span></div>
            <div className="ov-bar"><div style={{ width: `${(node.cpuUsed / (node.cpuCapacity || 1)) * 100}%` }} /></div>
          </div>
          <div className="ov-stat">
            <div className="ov-label">Memory</div>
            <div className="ov-val">{fmtMem(node.memUsed, memUnit)} / {fmtMem(node.memCapacity, memUnit, true)} <span>{memUnit}</span></div>
            <div className="ov-bar"><div style={{ width: `${(node.memUsed / (node.memCapacity || 1)) * 100}%`, background: "var(--ns-3)" }} /></div>
          </div>
          <div className="ov-stat">
            <div className="ov-label">Pods</div>
            <div className="ov-val">{node.pods.length} <span>scheduled</span></div>
            <div className="ov-bar"><div style={{ width: `${(node.pods.length / 110) * 100}%`, background: "var(--ns-2)" }} /></div>
          </div>
        </div>
        {/* Own row, one column per resource, so no grid cell is left blank. */}
        {Object.keys(node.ext).length > 0 && (
          <div className="overlay-stats" style={{ gridTemplateColumns: `repeat(${Object.keys(node.ext).length}, 1fr)` }}>
            {Object.keys(node.ext).sort().map(key => {
              const used = node.pods.reduce((s, p) => s + (p.ext[key] || 0), 0);
              return (
                <div key={key} className="ov-stat" title={key}>
                  <div className="ov-label">{extMetric(key).label}</div>
                  <div className="ov-val">{fmtExt(used, key, memUnit)} / {fmtExt(node.ext[key], key, memUnit, true)} <span>{extUnit(key, memUnit)}</span></div>
                  <div className="ov-bar"><div style={{ width: `${(used / node.ext[key]) * 100}%` }} /></div>
                </div>
              );
            })}
          </div>
        )}
        {(node.warnings.length > 0 || node.taints.length > 0) && (
          <div className="overlay-sched">
            <div className="ov-section-title">Scheduling</div>
            <div className="ov-chips">
              {node.warnings.map(w => (
                <span key={w} className={`status-pill status-${warnInfo(w).pill}`}>{warnInfo(w).label}</span>
              ))}
              {node.warnings.length === 0 && (
                <span className="ov-chips-note">Schedulable — the taints below are advisory.</span>
              )}
            </div>
            {node.taints.length > 0 && (
              <div className="taint-rows">
                {node.taints.map((t, i) => (
                  <div key={i} className="taint-row">
                    <code>{t.key}{t.value ? `=${t.value}` : ""}</code>
                    <span className={`taint-effect eff-${t.effect}`}>{t.effect}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
        {strandNote && (
          <div className="overlay-sched">
            <div className="ov-section-title">Stranded capacity</div>
            <span className="ov-chips-note">{strandNote} at the median pod shape.</span>
          </div>
        )}
        {fitReasons && <FitVerdict reasons={fitReasons} />}
        <div className="overlay-sched">
          <div className="ov-section-title">Drain simulation</div>
          <button className="btn-primary" onClick={() => onDrain(node.name)}>Simulate drain</button>
        </div>
        <div className="overlay-pods">
          <div className="ov-section-title">Workloads</div>
          {node.pods.length === 0 && <div className="empty-state">Node has no scheduled pods.</div>}
          <div className="pod-rows">
            {node.pods.map((p, i) => {
              // Effective request from the backend, not a container sum —
              // init containers don't add on top of regular ones.
              const cpu = p.cpu;
              const mem = p.mem;
              return (
                <div key={i} className="pod-row">
                  <div className="pod-row-name">
                    <code>{p.name}</code>
                    <div className="pod-row-containers">
                      {p.containers.map((c, j) => (
                        <span key={j} className={`container-pill${c.init ? " init" : ""}`}>{c.name}</span>
                      ))}
                    </div>
                    {Object.keys(p.ext).length > 0 && (
                      <div className="pod-row-containers">
                        {Object.keys(p.ext).sort().map(key => (
                          <span key={key} className="container-pill" title={key}>
                            {extMetric(key).label} {fmtExt(p.ext[key], key, memUnit)}{isBytes(key) ? ` ${memUnit}` : ""}
                          </span>
                        ))}
                      </div>
                    )}
                    {p.findings.length > 0 && (
                      <div className="pod-row-findings">
                        {p.findings.map(f => <FindingPill key={f} f={f} />)}
                      </div>
                    )}
                    {p.resize && (
                      <div className="pod-row-containers">
                        <span className="container-pill" title={p.resize.message || undefined}>
                          wants {(p.resize.cpu / 1000).toFixed(2)} cores{p.resize.mem != null && ` · ${fmtMem(p.resize.mem, memUnit)} ${memUnit}`}
                        </span>
                      </div>
                    )}
                  </div>
                  <div className="pod-row-stat">
                    <span className="pod-row-num">{(cpu / 1000).toFixed(2)}</span>
                    <span className="pod-row-unit">cores</span>
                  </div>
                  <div className="pod-row-stat">
                    <span className="pod-row-num">{fmtMem(mem, memUnit)}</span>
                    <span className="pod-row-unit">{memUnit}</span>
                  </div>
                  <button className="btn" onClick={() => onLogs(p)} aria-label={`Logs of ${p.name}`}><Icon name="terminal" size={12} />Logs</button>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
