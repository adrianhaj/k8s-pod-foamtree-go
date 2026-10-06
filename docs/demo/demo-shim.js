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
  const DEMO_UNAVAILABLE = [
    "/api/fit",
    "/api/logs",
    "/api/llm/chat",
  ];
  const realFetch = window.fetch.bind(window);
  const data = name => new URL("data/" + name, document.baseURI).href;
  const json = (status, error) => new Response(JSON.stringify({ error }),
    { status, headers: { "Content-Type": "application/json" } });

  window.fetch = function (input, init) {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, location.origin);
    if (url.origin !== location.origin) return realFetch(input, init);
    if (url.pathname === "/api/drain") {
      return realFetch(data(`drain-${url.searchParams.get("node")}.json`)).then(r => r.ok ? r :
        json(404, "this node is not in the recorded demo"));
    }
    if (DEMO_UNAVAILABLE.includes(url.pathname)) {
      return Promise.resolve(json(501, "not available in the demo — install k8sfoams to try this"));
    }
    const file = DEMO_ROUTES[url.pathname];
    return file ? realFetch(data(file)) : realFetch(input, init);
  };
})();
