// The Logs tab: one container's logs, fetched only when the viewer opens or
// reloads them. App owns which pod is shown; this file owns the fetch.

const { useState, useEffect, useMemo, useRef } = React;
const { Icon } = window.k8sIcons;
const { clock } = window.k8sFormat;

const TAILS = [200, 500, 2000];

function logsURL(context, sel) {
  return "/api/logs?" + new URLSearchParams({
    context, namespace: sel.namespace, pod: sel.name, container: sel.container,
    tail: String(sel.tail), previous: sel.previous ? "1" : "0",
  });
}

// Treemap leaves are labels: init containers carry " (init)", and unclaimed
// pod-level budget is a leaf of its own, not a container.
function containerNames(pod) {
  return pod.containers.filter(c => c.name !== "(pod-level)").map(c => c.name.replace(/ \(init\)$/, ""));
}

function selFor(pod, container) {
  const regular = pod.containers.find(c => !c.init && c.name !== "(pod-level)");
  const name = container || (regular ? regular.name : containerNames(pod)[0] || "");
  return { namespace: pod.namespace, name: pod.name, container: name, previous: false, tail: TAILS[0] };
}

// Highlight, never remove: matches light up and every line stays.
function markLine(line, q) {
  if (!q) return line;
  return line.split(q).flatMap((part, i) => (i === 0 ? [part] : [<mark key={i}>{q}</mark>, part]));
}

function LogsTab({ context, sel, setSel, pods, onLoaded, onAnalyze }) {
  const [state, setState] = useState({ url: null, text: null, error: null, busy: false, at: null });
  const [filter, setFilter] = useState("");
  const [tick, setTick] = useState(0);
  const key = sel ? `${sel.namespace}/${sel.name}` : "";
  const [podText, setPodText] = useState(key);
  useEffect(() => setPodText(key), [key]);
  const byKey = useMemo(() => new Map(pods.map(p => [`${p.namespace}/${p.name}`, p])), [pods]);
  const url = sel && sel.container ? logsURL(context, sel) : null;
  const loaded = useRef(null);
  // Lines belong to the url that fetched them; another selection shows none.
  const cur = state.url === url ? state : { text: null, error: null, at: null };

  useEffect(() => {
    if (!url) return;
    const ctl = new AbortController();
    if (loaded.current !== url) onLoaded(null);
    setState(s => (s.url === url ? { ...s, busy: true, error: null } : { url, text: null, error: null, busy: true, at: null }));
    fetch(url, { signal: ctl.signal })
      .then(async r => {
        if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
        return r.text();
      })
      .then(text => {
        const at = Date.now();
        loaded.current = url;
        setState({ url, text, error: null, busy: false, at });
        onLoaded({ ...sel, text, at });
      })
      .catch(e => {
        if (e.name === "AbortError") return;
        loaded.current = url;
        setState({ url, text: null, error: e.message, busy: false, at: null });
        onLoaded(null);
      });
    return () => ctl.abort();
  }, [url, tick]);

  const pickPod = v => {
    setPodText(v);
    const p = byKey.get(v);
    if (p) setSel(selFor(p));
  };
  const pod = sel && byKey.get(key);
  const lines = cur.text ? cur.text.replace(/\n$/, "").split("\n") : [];
  const hits = filter ? lines.filter(l => l.includes(filter)).length : 0;

  return (
    <>
      <div className="panel-bar">
        <input id="log-pod" className="mini-search wide" list="log-pods" value={podText} spellCheck="false"
          placeholder="namespace/pod" aria-label="Pod" onChange={e => pickPod(e.target.value)} />
        <datalist id="log-pods">{[...byKey.keys()].sort().map(k => <option key={k} value={k} />)}</datalist>
        {pod && (
          <select id="log-container" aria-label="Container" value={sel.container} onChange={e => setSel({ ...sel, container: e.target.value })}>
            {containerNames(pod).map(n => <option key={n} value={n}>{n}</option>)}
          </select>
        )}
        <div className="seg" role="radiogroup" aria-label="Run">
          {[[false, "Current"], [true, "Previous"]].map(([prev, label]) => (
            <button key={label} role="radio" aria-checked={!!sel && sel.previous === prev} disabled={!sel}
              className={sel && sel.previous === prev ? "seg-on" : ""} onClick={() => setSel({ ...sel, previous: prev })}>{label}</button>
          ))}
        </div>
        <select id="log-tail" aria-label="Lines" disabled={!sel} value={sel ? sel.tail : TAILS[0]} onChange={e => setSel({ ...sel, tail: Number(e.target.value) })}>
          {TAILS.map(n => <option key={n} value={n}>Last {n} lines</option>)}
        </select>
        <input id="log-filter" className="mini-search" value={filter} spellCheck="false" placeholder="Highlight text"
          aria-label="Highlight text" onChange={e => setFilter(e.target.value)} />
        <button className="btn-quiet" disabled={!url || state.busy} onClick={() => setTick(t => t + 1)}><Icon name="refresh" size={14} />Reload</button>
        {onAnalyze && (
          <>
            <span className="grow" />
            <button className="btn-primary" disabled={!cur.text} onClick={onAnalyze}><Icon name="spark" size={14} />Analyze with assistant</button>
          </>
        )}
      </div>
      {!sel ? <div className="panel-empty">Pick a pod above, or use Logs on a selected workload or in a node's pod list. Logs load only when you open or reload them.</div>
        : cur.error ? <div className="panel-empty sim-error">{cur.error}</div>
        : cur.text == null ? <div className="panel-empty">Loading logs…</div>
        : cur.text === "" ? <div className="panel-empty">The container wrote no log lines.</div>
        : (
          <div className="logview" role="log" aria-label={`Logs of ${key}, container ${sel.container}`}>
            {lines.map((l, i) => <div key={i} className="logline"><b>{i + 1}</b><span>{markLine(l, filter)}</span></div>)}
            <div className="logfoot">
              Last {lines.length} lines of the {sel.previous ? "previous" : "current"} run of {sel.container}
              {filter ? ` · ${hits} line${hits === 1 ? "" : "s"} match “${filter}”` : ""} · fetched {clock(cur.at)}
            </div>
          </div>
        )}
    </>
  );
}

window.k8sLogs = { LogsTab, logsURL, markLine, selFor, containerNames, TAILS };
