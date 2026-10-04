// Assistant plumbing without UI: connection settings, the context sent with
// a question, token estimates and the streamed reply. assistant.jsx draws it.

const CONN_KEY = "k8sfoams.llm";
const SECRET_KEY = "k8sfoams.llm.key";
const CHARS_PER_TOKEN = 4; // the server estimates the same way
const MIN_ANSWER_TOKENS = 256; // the server refuses a question that leaves less room than this
const LOG_CONTEXT_CHARS = 32 * 1024;
const PROBLEM_ROWS = 50;
const DEFAULT_BUDGET = 20000;
const MIN_BUDGET = 1000;

const SYSTEM = "You are the assistant inside k8sfoams, a read-only Kubernetes dashboard of requested CPU and memory. "
  + "You cannot change the cluster: suggest kubectl commands for the viewer to run. "
  + "Name pods as namespace/name and nodes by name. Be brief.";

const estimateTokens = text => Math.ceil([...text].length / CHARS_PER_TOKEN);
const estimateMessages = msgs =>
  Math.floor(msgs.reduce((n, m) => n + [...m.role].length + [...m.content].length, 0) / CHARS_PER_TOKEN) + 1;
const fmtTokens = n => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));
const overCap = (tokens, budget) => budget > 0 && tokens + MIN_ANSWER_TOKENS > budget;

function safeSession() {
  try { return window.sessionStorage; } catch (e) { return null; }
}

function drop(storage, key) {
  try { if (storage) storage.removeItem(key); } catch (e) { /* best effort */ }
}

const CONN_DEFAULT = { mode: "server", url: "", model: "", remember: false, budget: DEFAULT_BUDGET };
const validConn = v => !!v && ["server", "own"].includes(v.mode) && typeof v.url === "string"
  && typeof v.model === "string" && typeof v.remember === "boolean" && Number.isInteger(v.budget) && v.budget >= MIN_BUDGET;

function loadConn() {
  const { safeStorage, readPref } = window.k8sPrefs;
  const c = readPref(safeStorage(), CONN_KEY, CONN_DEFAULT, validConn);
  const key = readPref(c.remember ? safeStorage() : safeSession(), SECRET_KEY, "", v => typeof v === "string");
  return { ...c, key };
}

// The key goes to sessionStorage unless the viewer ticked Remember.
function saveConn(c) {
  const { safeStorage, writePref } = window.k8sPrefs;
  const { key, ...rest } = c;
  writePref(safeStorage(), CONN_KEY, rest);
  writePref(c.remember ? safeStorage() : safeSession(), SECRET_KEY, key);
  drop(c.remember ? safeSession() : safeStorage(), SECRET_KEY);
}

function forgetKey() {
  drop(safeSession(), SECRET_KEY);
  drop(window.k8sPrefs.safeStorage(), SECRET_KEY);
}

const connReady = (c, server) => (c.mode === "server" ? !!(server && server.server) : !!(c.url && c.model));

// On the server connection the operator's cap wins when it is lower.
const effectiveBudget = (c, server) =>
  c.mode === "server" && server && server.maxTokens > 0 ? Math.min(c.budget, server.maxTokens) : c.budget;

const gib = mib => (mib / 1024).toFixed(1);

function contextItems({ context, totals, problems, pod, logs, node }) {
  const items = [{
    id: "summary", label: "Cluster summary", sub: `${totals.nodes} nodes · ${totals.pods} pods`,
    text: `Context ${context}: ${totals.nodes} nodes, ${totals.pods} pods. CPU requested ${(totals.cpuUsed / 1000).toFixed(1)} of ${(totals.cpuCap / 1000).toFixed(1)} cores. Memory requested ${gib(totals.memUsed)} of ${gib(totals.memCap)} GiB.`,
  }];
  if (problems.length) {
    const rows = problems.slice(0, PROBLEM_ROWS).map(r => `${r.sev} ${r.rule} ${r.object} on ${r.node}: ${r.detail}`);
    if (problems.length > PROBLEM_ROWS) rows.push(`(${problems.length - PROBLEM_ROWS} more not listed)`);
    items.push({ id: "problems", label: "Problems", sub: `${problems.length} findings, worst first`, text: rows.join("\n") });
  }
  if (pod) {
    const restarts = (pod.statuses || []).reduce((s, x) => s + x.restarts, 0);
    items.push({
      id: "pod", label: `Pod ${pod.namespace}/${pod.name}`, sub: `spec, status, ${restarts} restarts`,
      text: JSON.stringify({
        namespace: pod.namespace, name: pod.name, node: pod.node, phase: pod.phase, qos: pod.qos,
        cpuRequestMillicores: pod.cpu, memoryRequestMiB: Math.round(pod.mem),
        cpuLimitMillicores: pod.cpuLimit, memoryLimitMiB: pod.memLimit == null ? null : Math.round(pod.memLimit),
        containers: pod.containers.map(c => c.name), statuses: pod.statuses, findings: pod.findings,
      }),
    });
  }
  if (logs) {
    items.push({
      id: "logs", label: `Logs · ${logs.container}, ${logs.previous ? "previous" : "current"} run`, sub: `last ${logs.tail} lines`,
      text: `Logs of ${logs.namespace}/${logs.name}, container ${logs.container}, ${logs.previous ? "previous" : "current"} run:\n${logs.text.slice(-LOG_CONTEXT_CHARS)}`,
    });
  }
  if (node) {
    items.push({
      id: "node", label: `Node ${node.name}`, sub: `${node.pods.length} pods · ${node.warnings.join(", ") || "healthy"}`,
      text: `${node.name}: ${node.instanceType || "unknown type"}, zone ${node.zone || "none"}, pool ${node.pool || "none"}. CPU requested ${(node.cpuUsed / 1000).toFixed(2)} of ${(node.cpuCapacity / 1000).toFixed(1)} cores, memory ${gib(node.memUsed)} of ${gib(node.memCapacity)} GiB, ${node.pods.length} pods. Warnings: ${node.warnings.join(", ") || "none"}.`,
    });
  }
  return items.map(i => ({ ...i, tokens: estimateTokens(i.text) }));
}

// Everything is ticked by default except the node, which is only context
// the viewer opened earlier.
const isOn = (ticked, id) => ticked[id] ?? id !== "node";

function systemPrompt(items, ticked) {
  const parts = items.filter(i => isOn(ticked, i.id)).map(i => `## ${i.label}\n${i.text}`);
  return [SYSTEM, ...parts].join("\n\n");
}

function parseSSE(buf) {
  const blocks = buf.split("\n\n");
  const rest = blocks.pop();
  const events = [];
  for (const b of blocks) {
    for (const line of b.split("\n")) {
      if (!line.startsWith("data:")) continue;
      try { events.push(JSON.parse(line.slice(5).trim())); } catch (e) { /* skip a malformed line */ }
    }
  }
  return { events, rest };
}

async function streamChat({ conn, context, messages, signal, onEvent }) {
  const headers = { "Content-Type": "application/json" };
  const body = { context, budget: conn.budget, messages };
  if (conn.mode === "own") {
    Object.assign(body, { url: conn.url, model: conn.model });
    if (conn.key) headers["X-LLM-Key"] = conn.key;
  }
  const r = await fetch("/api/llm/chat", { method: "POST", headers, body: JSON.stringify(body), signal });
  if (r.status === 401) throw new Error("Your session expired. Reload the page to sign in again.");
  if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
  const reader = r.body.getReader();
  const dec = new TextDecoder();
  let rest = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    const out = parseSSE(rest + dec.decode(value, { stream: true }));
    rest = out.rest;
    out.events.forEach(onEvent);
  }
}

window.k8sLLM = {
  SYSTEM, DEFAULT_BUDGET, MIN_ANSWER_TOKENS, estimateTokens, estimateMessages, fmtTokens, loadConn, saveConn, forgetKey, connReady,
  effectiveBudget, overCap, contextItems, isOn, systemPrompt, parseSSE, streamChat,
};
