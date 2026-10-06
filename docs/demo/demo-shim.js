// Answers the console's API from files recorded at build time, so /demo/ needs no server.
// record.sh saves each route under the name dataFile gives it; a route with no file is not in the demo.
(function () {
  const realFetch = window.fetch.bind(window);
  const data = name => new URL("data/" + name, document.baseURI).href;
  const dataFile = path => {
    const name = path.slice(1).replaceAll("/", "-");
    return name.includes(".") ? name : name + ".json";
  };
  // Plain text, like the server's http.Error: the console shows the body as is.
  const text = (status, msg) => new Response(msg, { status, headers: { "Content-Type": "text/plain" } });
  const recorded = (name, status, missing) => realFetch(data(name)).then(r => r.ok ? r : text(status, missing));

  window.fetch = function (input, init) {
    const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(raw, location.origin);
    if (url.origin !== location.origin) return realFetch(input, init);
    if (url.pathname === "/api/drain") {
      return recorded(`drain-${url.searchParams.get("node")}.json`, 404, "this node is not in the recorded demo");
    }
    return recorded(dataFile(url.pathname), 501, "not available in the demo — install k8sfoams to try this");
  };

  // The Export menu links to /report.* with plain anchors, which fetch can't intercept.
  document.addEventListener("click", e => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a) return;
    const url = new URL(a.href, location.origin);
    if (url.origin === location.origin && url.pathname.startsWith("/report.")) a.href = data(dataFile(url.pathname));
  }, true);

  document.addEventListener("DOMContentLoaded", () => {
    const bar = document.createElement("div");
    bar.style.cssText = "position:fixed;right:12px;bottom:12px;z-index:9999;padding:6px 12px;border-radius:6px;box-shadow:0 2px 8px rgba(0,0,0,.25);font:500 13px 'IBM Plex Sans',sans-serif;background:#1f5fd6;color:#fff";
    bar.innerHTML = 'Demo · synthetic cluster, recorded at build time · <a href="../docs/getting-started/install/" style="color:#fff;text-decoration:underline">Install k8sfoams</a>';
    document.body.append(bar);
  });
})();
