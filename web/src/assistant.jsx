// The Assistant tab: transcript, composer, context column and connection
// form. Only Send, Analyze with assistant and Test and save reach the model.

const { useState, useRef, useMemo, useEffect, useCallback, useSyncExternalStore } = React;
const { Icon, SevGlyph } = window.k8sIcons;
const { podKey } = window.k8sWorkload;
const {
  DEFAULT_BUDGET, MIN_BUDGET, msgChars, estimateMessages, fmtTokens, loadConn, saveConn, forgetKey, connReady, effectiveBudget,
  overCap, contextItems, isOn, systemPrompt, streamChat,
} = window.k8sLLM;
const { safeStorage, readPref, writePref } = window.k8sPrefs;

const ACK_KEY = "k8sfoams.llm.ack";
const QUICK = ["Explain the problems", "Which nodes have the least room left?", "Which pods have no memory limit?"];

function applyEvent(ms, ev) {
  const last = { ...ms[ms.length - 1] };
  if (ev.type === "delta") last.content += ev.text;
  else if (ev.type === "tool") last.tools = [...last.tools, `${ev.name} ${ev.args || ""}`.trim()];
  else if (ev.type === "notice") last.note = ev.text;
  else if (ev.type === "error") last.error = ev.text;
  else if (ev.type === "done") { last.usage = ev.usage; last.masked = ev.masked || 0; }
  else if (ev.type === "cut") last.cut = true;
  return [...ms.slice(0, -1), last];
}

// The composer text lives outside the dashboard's state, so typing
// re-renders the composer, not everything around the hook's owner.
function textStore() {
  let v = "";
  const subs = new Set();
  return {
    get: () => v,
    set: f => { v = typeof f === "function" ? f(v) : f; subs.forEach(s => s()); },
    sub: s => { subs.add(s); return () => subs.delete(s); },
  };
}

function knownOf(nodes) {
  const m = new Map();
  for (const n of nodes) {
    m.set(n.name, n);
    for (const p of n.pods) m.set(podKey(p), n);
  }
  return m;
}

// The fixed part of a request: the system prompt with the ticked context, then
// earlier turns. plan adds the question; the estimate covers exactly the array
// that goes out, the way the server counts it.
function head(items, tick, messages) {
  const wire = [
    { role: "system", content: systemPrompt(items, tick) },
    ...messages.filter(m => m.content).map(m => ({ role: m.role, content: m.content })),
  ];
  return { wire, chars: msgChars(wire), labels: items.filter(i => isOn(tick, i.id)).map(i => i.label) };
}

function plan(h, text) {
  const user = { role: "user", content: text };
  return { wire: [...h.wire, user], tokens: estimateMessages([user], h.chars), labels: h.labels };
}

// Everything the Assistant needs from the dashboard. Context items and name
// links are built only while the tab is open; Analyze builds them on demand.
function useAssistant({ context, nodes, totals, problems, focused, logsPod, logsText, open, setPanel, setFocused }) {
  const [conn, setConn] = useState(loadConn);
  const [server, setServer] = useState(null);
  useEffect(() => {
    fetch("/api/llm/config").then(r => (r.ok ? r.json() : null)).then(setServer, () => setServer(null));
  }, []);
  const [messages, setMessages] = useState([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [draft] = useState(textStore);
  const [ticked, setTicked] = useState({});
  const [connecting, setConnecting] = useState(false);
  const [acked, setAcked] = useState(() => readPref(safeStorage(), ACK_KEY, false, v => v === true));
  const [lastNode, setLastNode] = useState(null);
  useEffect(() => { if (focused) setLastNode(focused.name); }, [focused]);
  const abort = useRef(null);

  const logsPodObj = useMemo(() => {
    if (!logsPod) return null;
    for (const n of nodes) {
      const p = n.pods.find(x => x.namespace === logsPod.namespace && x.name === logsPod.name);
      if (p) return { ...p, node: n.name };
    }
    return null;
  }, [nodes, logsPod]);
  const logsForPod = logsText && logsPod && logsText.namespace === logsPod.namespace && logsText.name === logsPod.name ? logsText : null;
  const itemsWith = k => contextItems({
    context, totals, problems, pod: logsPodObj, logs: logsForPod, node: (lastNode && k.get(lastNode)) || null,
  });
  const known = useMemo(() => (open ? knownOf(nodes) : null), [open, nodes]);
  const items = useMemo(() => (known ? itemsWith(known) : null), [known, context, totals, problems, logsPodObj, logsForPod, lastNode]);
  const pick = useCallback(name => setFocused((known && known.get(name)) || null), [known, setFocused]);

  const send = async (p, text) => {
    setMessages([...messages, { role: "user", content: text, sent: { tokens: p.tokens, items: p.labels } }, { role: "assistant", content: "", tools: [] }]);
    draft.set("");
    setBusy(true);
    setError(null);
    const ctl = new AbortController();
    abort.current = ctl;
    let ended = false, got = false, failed = null, note = null;
    try {
      await streamChat({
        conn, context, messages: p.wire, signal: ctl.signal,
        onEvent: ev => {
          if (ev.type === "done" || ev.type === "error") ended = true;
          if (ev.type === "error") failed = ev.text;
          if (ev.type === "delta" && ev.text) got = true;
          if (ev.type === "notice") note = ev.text;
          setMessages(ms => applyEvent(ms, ev));
        },
      });
      if (!ended) setMessages(ms => applyEvent(ms, { type: "cut" }));
    } catch (e) {
      if (e.name !== "AbortError") failed = e.message;
    } finally {
      setBusy(false);
      abort.current = null;
    }
    // A question that got no answer text (failed, stopped, or empty) is taken
    // back, so the retry keeps user and assistant turns alternating, which
    // strict chat templates require. Text typed meanwhile is kept.
    if (!got) {
      setMessages(ms => ms.slice(0, -2));
      draft.set(d => (d.trim() ? d : text));
      setError(failed || note);
    } else if (failed && !ended) setError(failed);
  };

  const ask = (its, tick, text) => {
    const p = plan(head(its, tick, messages), text);
    if (busy || overCap(p.tokens, effectiveBudget(conn, server))) {
      draft.set(text);
      return;
    }
    send(p, text);
  };

  // The one click in the Logs tab that spends tokens.
  const analyzePod = () => {
    if (!logsPodObj) return;
    const tick = { ...ticked, pod: true, logs: true };
    setTicked(tick);
    setPanel(p => ({ ...p, open: true, tab: "assistant", max: true }));
    const q = `Analyze pod ${podKey(logsPodObj)}. Use its status and the logs provided. What is the most likely cause, and how do I fix it?`;
    // Until the privacy notice is acknowledged, the question waits in the composer.
    if (!acked || !connReady(conn, server)) {
      draft.set(q);
      return;
    }
    setConnecting(false);
    ask(itemsWith(known || knownOf(nodes)), tick, q);
  };

  return {
    conn, setConn, server, context, items, known, pick, messages, busy, error, draft, ticked, setTicked,
    connecting, setConnecting, ask, analyzePod, acked,
    ack: () => { writePref(safeStorage(), ACK_KEY, true); setAcked(true); },
    openConnection: () => { setConnecting(true); setPanel(p => ({ ...p, open: true, tab: "assistant" })); },
    stop: () => abort.current && abort.current.abort(),
    clear: () => { setMessages([]); setError(null); },
  };
}

// Model output becomes React text: fenced blocks turn into <pre>, `code` into
// <code>, and names the snapshot knows into links. Never innerHTML.
function renderText(text, known, onPick) {
  return text.split(/```[\w-]*\n?/).map((part, i) => (i % 2 ? <pre key={i}>{part}</pre> : <p key={i}>{inline(part, known, onPick)}</p>));
}

function inline(s, known, onPick) {
  return s.split(/(`[^`\n]+`)/).map((seg, i) => {
    if (i % 2) return <code key={i}>{seg.slice(1, -1)}</code>;
    return seg.split(/(\s+)/).map((w, j) => {
      const bare = w.replace(/[.,;:!?)]+$/, "");
      if (!known.has(bare)) return w;
      return (
        <React.Fragment key={`${i}-${j}`}>
          <button className="obj" onClick={() => onPick(bare)}>{bare}</button>{w.slice(bare.length)}
        </React.Fragment>
      );
    });
  });
}

const usageText = u => (u.estimated
  ? `≈ ${fmtTokens(u.total)} tokens, estimated: the endpoint did not report usage`
  : `used ${u.total.toLocaleString()} tokens (${u.prompt.toLocaleString()} in · ${u.completion.toLocaleString()} out)`);

function PrivacyNotice({ onOk }) {
  return (
    <div className="notice"><SevGlyph sev="info" /><span><b>What leaves this cluster.</b> Your questions and the context you tick are sent to the chosen endpoint. Pod specs and logs can contain secrets. Values that look like secrets, such as passwords, tokens, keys and credentials in URLs, are masked on the server before anything is sent. Masking matches patterns and can miss an unusual secret. Nothing is sent until you press Send, Analyze with assistant, or Test and save (which sends one short test message).</span>
      {onOk && <button className="btn" onClick={onOk}>OK</button>}</div>
  );
}

function ConnectForm({ conn, server, context, onSaved, onBack, onForget, onAck }) {
  const hasServer = !!(server && server.server);
  const [f, setF] = useState(() => ({ ...conn, mode: hasServer ? conn.mode : "own" }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [forgot, setForgot] = useState(false);
  const set = (k, v) => setF(x => ({ ...x, [k]: v }));
  const clean = () => ({ ...f, budget: Math.max(MIN_BUDGET, Math.round(Number(f.budget)) || DEFAULT_BUDGET) });
  const save = c => { saveConn(c); onSaved(c); };

  const test = async e => {
    e.preventDefault();
    const c = clean();
    setBusy(true);
    setError(null);
    try {
      // One tiny question proves URL, model and key before anything real is sent.
      let done = false, got = false, note = null;
      await streamChat({
        conn: c, context, messages: [{ role: "user", content: "Reply with the single word OK." }],
        onEvent: ev => {
          if (ev.type === "error") throw new Error(ev.text);
          if (ev.type === "delta" && ev.text) got = true;
          if (ev.type === "notice") note = ev.text;
          if (ev.type === "done") done = true;
        },
      });
      if (!got) throw new Error(`No answer text came back${note ? `: ${note}` : ""}. Check the base URL and model.`);
      if (!done) throw new Error("The answer ended before the endpoint finished.");
      save(c);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const capTag = f.mode === "server" && server.maxTokens > 0
    ? <> <span className="tag">max {server.maxTokens.toLocaleString()}</span></> : null;
  const budget = (
    <label className="sim-field narrow"><span>Token cap per question{capTag}</span>
      <input id="llm-budget" type="number" min={MIN_BUDGET} step="1000" value={f.budget} onChange={e => set("budget", e.target.value)} /></label>
  );
  const back = onBack && !forgot && <button className="btn" type="button" onClick={onBack}>Back to chat</button>;

  return (
    <div className="connect">
      <PrivacyNotice onOk={onAck} />
      {hasServer && (
        <div className="panel-bar">
          <div className="seg" role="radiogroup" aria-label="Connection">
            {[["server", "Server connection"], ["own", "My own key"]].map(([m, l]) => (
              <button key={m} role="radio" aria-checked={f.mode === m} className={f.mode === m ? "seg-on" : ""} onClick={() => set("mode", m)}>{l}</button>
            ))}
          </div>
        </div>
      )}
      {f.mode === "server" ? (
        <>
          <div className="sim-row">
            <label className="sim-field"><span>Base URL <span className="tag">set by operator</span></span><input id="srv-url" value={server.url} readOnly /></label>
            <label className="sim-field narrow"><span>Model <span className="tag">set by operator</span></span><input id="srv-model" value={server.model} readOnly /></label>
            <label className="sim-field narrow"><span>API key</span><input id="srv-key" value="•••• on server" readOnly /></label>
            {budget}
            <button className="btn-primary" type="button" onClick={() => save(clean())}>Use server connection</button>
            {back}
          </div>
          <p className="panel-note">The key stays on the server. Your browser never receives it, and it is only ever sent to the operator's URL.</p>
        </>
      ) : (
        <>
          <form className="sim-form sim-row" onSubmit={test}>
            <label className="sim-field"><span>Base URL (OpenAI-compatible)</span><input id="llm-url" value={f.url} spellCheck="false" placeholder="https://api.openai.com/v1" onChange={e => set("url", e.target.value)} /></label>
            <label className="sim-field narrow"><span>Model</span><input id="llm-model" value={f.model} spellCheck="false" placeholder="gpt-4.1-mini" onChange={e => set("model", e.target.value)} /></label>
            <label className="sim-field"><span>API key</span><input id="llm-key" type="password" value={f.key} spellCheck="false" autoComplete="off" onChange={e => set("key", e.target.value)} /></label>
            {budget}
            <label className="check"><input id="llm-remember" type="checkbox" checked={f.remember} onChange={e => set("remember", e.target.checked)} />Remember key in this browser</label>
            <button className="btn-primary" type="submit" disabled={busy || !f.url || !f.model}>{busy ? "Testing…" : "Test and save"}</button>
            <button className="btn" type="button" onClick={() => { forgetKey(); set("key", ""); onForget(); setForgot(true); }}>Forget key</button>
            {back}
          </form>
          {error && <p className="panel-note sim-error">{error}</p>}
          <p className="panel-note">Unticked, the key lasts until you close this tab. Ticked, it stays in this browser's storage, where browser extensions and anyone using this computer profile can read it. Works with OpenAI, Azure OpenAI, OpenRouter, vLLM and Ollama (<code>http://localhost:11434/v1</code>, no key).</p>
        </>
      )}
    </div>
  );
}

function AssistantTab({ a }) {
  const ready = connReady(a.conn, a.server);
  if (!ready || a.connecting) {
    return <ConnectForm conn={a.conn} server={a.server} context={a.context} onBack={ready ? () => a.setConnecting(false) : null} onAck={a.acked ? null : a.ack}
      onSaved={c => { a.setConn(c); a.setConnecting(false); }} onForget={() => a.setConn(c => ({ ...c, key: "" }))} />;
  }
  return <Chat a={a} />;
}

// Memoized, so a streamed delta re-renders only the message it lands in.
const Msg = React.memo(function Msg({ m, streaming, known, onPick }) {
  return (
    <div className={`msg${m.role === "user" ? " you" : ""}`}>
      <div className="msg-role">{m.role === "user" ? "You" : "Assistant"}</div>
      <div className="msg-body">
        {(m.tools || []).map((t, j) => <div key={j} className="tool">{t}</div>)}
        {renderText(m.content, known, onPick)}
        {streaming && <span className="caret" />}
        {m.note && <div className="msg-meta">{m.note}</div>}
        {m.cut && <div className="msg-meta">The answer ended before the endpoint finished.</div>}
        {m.error && <div className="sim-error">{m.error}</div>}
        {m.sent && <div className="msg-meta">sent with: {m.sent.items.join(", ").toLowerCase()} · ≈ {fmtTokens(m.sent.tokens)} tokens</div>}
        {m.usage && <div className="msg-meta">{usageText(m.usage)}</div>}
        {m.masked > 0 && <div className="msg-meta">{m.masked} likely secret{m.masked === 1 ? "" : "s"} masked before sending</div>}
      </div>
    </div>
  );
});

function Chat({ a }) {
  const { conn, server, items } = a;
  const srv = conn.mode === "server";
  const draft = useSyncExternalStore(a.draft.sub, a.draft.get);
  const h = useMemo(() => head(items, a.ticked, a.messages), [items, a.ticked, a.messages]);
  const budget = effectiveBudget(conn, server);
  const next = plan(h, draft);
  const over = overCap(next.tokens, budget);
  const used = a.messages.reduce((s, m) => s + (m.usage ? m.usage.total : 0), 0);
  const submit = () => {
    const text = draft.trim();
    if (text && !a.busy && !over) a.ask(items, a.ticked, text);
  };
  const base = srv ? server.url : conn.url;
  let host = base;
  try { host = new URL(base).host; } catch (e) { /* show it as typed */ }

  return (
    <div className="ast">
      <div className="ast-main">
        {!a.acked && <PrivacyNotice onOk={a.ack} />}
        <div className="transcript" aria-live="polite">
          {a.messages.length === 0 && <div className="panel-empty">Ask about this cluster. Tick what to send in the context column. Nothing is sent until you press Send.</div>}
          {a.messages.map((m, i) => (
            <Msg key={i} m={m} streaming={a.busy && i === a.messages.length - 1} known={a.known} onPick={a.pick} />
          ))}
          {a.error && <div className="panel-empty sim-error">{a.error}</div>}
        </div>
        <div className="composer">
          <div className="panel-chips">{QUICK.map(q => <button key={q} onClick={() => a.draft.set(q)}>{q}</button>)}</div>
          <div className="composer-row">
            <textarea id="ast-input" rows="2" value={draft} aria-label="Message"
              placeholder="Ask about this cluster. Enter sends, Shift+Enter adds a line."
              onChange={e => a.draft.set(e.target.value)}
              onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) { e.preventDefault(); submit(); } }} />
            <div className="composer-side">
              <span className="cost" title="Rough estimate: 4 characters per token">
                next message ≈ {fmtTokens(next.tokens)}{budget > 0 ? ` of ${fmtTokens(budget)} cap` : ""} tokens
              </span>
              {a.busy
                ? <button className="btn" onClick={a.stop}><Icon name="stop" size={12} />Stop</button>
                : <button className="btn-primary" disabled={!draft.trim() || over} onClick={submit}>Send</button>}
            </div>
          </div>
          {over && <div className="panel-note">Over the cap of {budget.toLocaleString()} tokens. Untick some context, or raise the cap in Connection.</div>}
        </div>
      </div>
      <aside className="ctx" aria-label="Context sent with your next message">
        <div className="menu-h">Sent with your next message</div>
        {items.map(i => (
          <label key={i.id} className={`ctx-item${isOn(a.ticked, i.id) ? "" : " off"}`}>
            <input type="checkbox" checked={isOn(a.ticked, i.id)} onChange={e => a.setTicked(t => ({ ...t, [i.id]: e.target.checked }))} />
            <span>{i.label}<small>{i.sub}</small></span><span className="k">{fmtTokens(i.tokens)}</span>
          </label>
        ))}
        <div className="ctx-foot">
          <div>Model <b>{srv ? server.model : conn.model}</b><br />at <b>{host}</b> · {srv ? "server key" : "your key"}</div>
          {used > 0 && <div>This chat: <b>{used.toLocaleString()}</b> tokens{budget > 0 ? <> · cap <b>{budget.toLocaleString()}</b> per question</> : null}</div>}
          <div className="acts">
            <button className="btn" onClick={() => a.setConnecting(true)}>Connection</button>
            <button className="btn" disabled={a.busy} onClick={a.clear}>Clear chat</button>
          </div>
        </div>
      </aside>
    </div>
  );
}

window.k8sAssistant = { useAssistant, AssistantTab };
