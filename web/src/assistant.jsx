// The Assistant tab: transcript, composer, context column and connection
// form. Only Send, Analyze with assistant and Test and save reach the model.

const { useState, useRef } = React;
const { Icon, SevGlyph } = window.k8sIcons;
const {
  DEFAULT_BUDGET, estimateMessages, fmtTokens, saveConn, forgetKey, connReady, effectiveBudget,
  overCap, isOn, systemPrompt, streamChat,
} = window.k8sLLM;

const QUICK = ["Explain the problems", "Which nodes have the least room left?", "Which pods have no memory limit?"];

function applyEvent(ms, ev) {
  const last = { ...ms[ms.length - 1] };
  if (ev.type === "delta") last.content += ev.text;
  else if (ev.type === "tool") last.tools = [...last.tools, `${ev.name} ${ev.args || ""}`.trim()];
  else if (ev.type === "notice") last.note = ev.text;
  else if (ev.type === "error") last.error = ev.text;
  else if (ev.type === "done") last.usage = ev.usage;
  else if (ev.type === "cut") last.cut = true;
  return [...ms.slice(0, -1), last];
}

function useAssistant(context) {
  const [messages, setMessages] = useState([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [draft, setDraft] = useState("");
  const [ticked, setTicked] = useState({});
  const [connecting, setConnecting] = useState(false);
  const abort = useRef(null);

  // The estimate covers exactly the array that goes out, the way the server counts it.
  const plan = (items, tick, text) => {
    const wire = [
      { role: "system", content: systemPrompt(items, tick) },
      ...messages.filter(m => m.content).map(m => ({ role: m.role, content: m.content })),
      { role: "user", content: text },
    ];
    return { wire, tokens: estimateMessages(wire), labels: items.filter(i => isOn(tick, i.id)).map(i => i.label) };
  };

  const send = async (conn, p, text) => {
    setMessages([...messages, { role: "user", content: text, sent: { tokens: p.tokens, items: p.labels } }, { role: "assistant", content: "", tools: [] }]);
    setDraft("");
    setBusy(true);
    setError(null);
    const ctl = new AbortController();
    abort.current = ctl;
    let ended = false, got = false, failed = null;
    try {
      await streamChat({
        conn, context, messages: p.wire, signal: ctl.signal,
        onEvent: ev => {
          if (ev.type === "done" || ev.type === "error") ended = true;
          if (ev.type === "error") failed = ev.text;
          if (ev.type === "delta" && ev.text) got = true;
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
    // A question that got no answer is taken back, so the retry keeps user and
    // assistant turns alternating, which strict chat templates require.
    if (failed && !got) {
      setMessages(ms => ms.slice(0, -2));
      setDraft(text);
      setError(failed);
    } else if (failed && !ended) setError(failed);
  };

  const ask = (conn, budget, items, tick, text) => {
    const p = plan(items, tick, text);
    if (busy || overCap(p.tokens, budget)) {
      setDraft(text);
      return;
    }
    send(conn, p, text);
  };

  return {
    messages, busy, error, draft, setDraft, ticked, setTicked, connecting, setConnecting, plan, ask,
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

function ConnectForm({ conn, server, context, onSaved, onBack, onForget }) {
  const hasServer = !!(server && server.server);
  const [f, setF] = useState(() => ({ ...conn, mode: hasServer ? conn.mode : "own" }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const [forgot, setForgot] = useState(false);
  const set = (k, v) => setF(x => ({ ...x, [k]: v }));
  const clean = () => ({ ...f, budget: Math.max(1000, Math.round(Number(f.budget)) || DEFAULT_BUDGET) });
  const save = c => { saveConn(c); onSaved(c); };

  const test = async e => {
    e.preventDefault();
    const c = clean();
    setBusy(true);
    setError(null);
    try {
      // One tiny question proves URL, model and key before anything real is sent.
      let done = false;
      await streamChat({
        conn: c, context, messages: [{ role: "user", content: "Reply with the single word OK." }],
        onEvent: ev => {
          if (ev.type === "error") throw new Error(ev.text);
          if (ev.type === "done") done = true;
        },
      });
      if (!done) throw new Error("The answer ended before the endpoint finished.");
      save(c);
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const capTag = f.mode === "server" && hasServer && server.maxTokens > 0
    ? <> <span className="tag">max {server.maxTokens.toLocaleString()}</span></> : null;
  const budget = (
    <label className="sim-field narrow"><span>Token cap per question{capTag}</span>
      <input id="llm-budget" type="number" min="1000" step="1000" value={f.budget} onChange={e => set("budget", e.target.value)} /></label>
  );
  const back = onBack && !forgot && <button className="btn" type="button" onClick={onBack}>Back to chat</button>;

  return (
    <div className="connect">
      <div className="notice"><SevGlyph sev="info" /><span><b>What leaves this cluster.</b> Your questions and the context you tick are sent to the chosen endpoint. Pod specs and logs can contain secrets. Nothing is sent until you press Send or Analyze with assistant.</span></div>
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

function AssistantTab({ a, conn, setConn, server, items, known, onPick, context }) {
  const ready = connReady(conn, server);
  if (!ready || a.connecting) {
    return <ConnectForm conn={conn} server={server} context={context} onBack={ready ? () => a.setConnecting(false) : null}
      onSaved={c => { setConn(c); a.setConnecting(false); }} onForget={() => setConn(c => ({ ...c, key: "" }))} />;
  }
  const budget = effectiveBudget(conn, server);
  const next = a.plan(items, a.ticked, a.draft);
  const over = overCap(next.tokens, budget);
  const used = a.messages.reduce((s, m) => s + (m.usage ? m.usage.total : 0), 0);
  const submit = () => {
    const text = a.draft.trim();
    if (text && !a.busy && !over) a.ask(conn, budget, items, a.ticked, text);
  };
  const base = conn.mode === "server" ? server.url : conn.url;
  let host = base;
  try { host = new URL(base).host; } catch (e) { /* show it as typed */ }

  return (
    <div className="ast">
      <div className="ast-main">
        <div className="transcript" aria-live="polite">
          {a.messages.length === 0 && <div className="panel-empty">Ask about this cluster. Tick what to send in the context column. Nothing is sent until you press Send.</div>}
          {a.messages.map((m, i) => (
            <div key={i} className={`msg${m.role === "user" ? " you" : ""}`}>
              <div className="msg-role">{m.role === "user" ? "You" : "Assistant"}</div>
              <div className="msg-body">
                {(m.tools || []).map((t, j) => <div key={j} className="tool">{t}</div>)}
                {renderText(m.content, known, onPick)}
                {a.busy && i === a.messages.length - 1 && <span className="caret" />}
                {m.note && <div className="msg-meta">{m.note}</div>}
                {m.cut && <div className="msg-meta">The answer ended before the endpoint finished.</div>}
                {m.error && <div className="sim-error">{m.error}</div>}
                {m.sent && <div className="msg-meta">sent with: {m.sent.items.join(", ").toLowerCase()} · ≈ {fmtTokens(m.sent.tokens)} tokens</div>}
                {m.usage && <div className="msg-meta">{usageText(m.usage)}</div>}
              </div>
            </div>
          ))}
          {a.error && <div className="panel-empty sim-error">{a.error}</div>}
        </div>
        <div className="composer">
          <div className="panel-chips">{QUICK.map(q => <button key={q} onClick={() => a.setDraft(q)}>{q}</button>)}</div>
          <div className="composer-row">
            <textarea id="ast-input" rows="2" value={a.draft} aria-label="Message"
              placeholder="Ask about this cluster. Enter sends, Shift+Enter adds a line."
              onChange={e => a.setDraft(e.target.value)}
              onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); } }} />
            <div className="composer-side">
              <span className="cost" title="Rough estimate: 4 characters per token">
                next message ≈ {fmtTokens(next.tokens)}{budget > 0 ? ` of ${fmtTokens(budget)} cap` : ""} tokens
              </span>
              {a.busy
                ? <button className="btn" onClick={a.stop}><Icon name="stop" size={12} />Stop</button>
                : <button className="btn-primary" disabled={!a.draft.trim() || over} onClick={submit}>Send</button>}
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
          <div>Model <b>{conn.mode === "server" ? server.model : conn.model}</b><br />at <b>{host}</b> · {conn.mode === "server" ? "server key" : "your key"}</div>
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

window.k8sAssistant = { useAssistant, AssistantTab, renderText };
