// Answers the console's API from files recorded at build time, so /demo/ needs no server.
(function () {
  const DEMO_ROUTES = {
    "/contexts": "contexts.json",
    "/api/me": "me.json",
    "/api/llm/config": "llm-config.json",
    "/resources/cpu": "resources-cpu.json",
    "/resources/memory": "resources-memory.json",
    "/report.json": "report.json",
    "/report.csv": "report.csv",
  };
  const realFetch = window.fetch.bind(window);
  const data = name => new URL("data/" + name, document.baseURI).href;
  // Plain text, like the server's http.Error: the console shows the body as is.
  const text = (status, msg) => new Response(msg, { status, headers: { "Content-Type": "text/plain" } });
  const unavailable = () => text(501, "not available in the demo — install k8sfoams to try this");
  const recorded = (name, missing) => realFetch(data(name)).then(r => r.ok ? r : text(404, missing));

  window.fetch = function (input, init) {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, location.origin);
    if (url.origin !== location.origin) return realFetch(input, init);
    if (url.pathname === "/api/drain") {
      return recorded(`drain-${url.searchParams.get("node")}.json`, "this node is not in the recorded demo");
    }
    const file = DEMO_ROUTES[url.pathname];
    if (file) return recorded(file, "recorded demo data is missing");
    // /api/fit, /api/logs, /api/llm/chat and anything new land here
    return Promise.resolve(unavailable());
  };

  // The Export menu links to /report.* with plain anchors, which fetch can't intercept.
  document.addEventListener("click", e => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a) return;
    const url = new URL(a.href, location.origin);
    const file = url.origin === location.origin && DEMO_ROUTES[url.pathname];
    if (file) a.href = data(file);
  }, true);
})();
