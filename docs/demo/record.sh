#!/usr/bin/env bash
# Records the console's API from --synthetic so /demo/ runs with no backend.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
out="$root/docs/static/demo"
port=${DEMO_PORT:-18090}
if lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then echo "port $port is busy" >&2; exit 1; fi
rm -rf "$out" && mkdir -p "$out/data"
cp -R "$root/web/static/." "$out/"

log=$(mktemp)
"$root/bin/k8sfoams" --synthetic 20x10 --port "$port" >"$log" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true; rm -f "$log"' EXIT
healthy=
for _ in $(seq 50); do curl -fs "localhost:$port/healthcheck" >/dev/null && { healthy=1; break; }; sleep 0.2; done
if [ -z "$healthy" ] || ! kill -0 $pid 2>/dev/null; then echo "k8sfoams did not start on :$port" >&2; cat "$log" >&2; exit 1; fi

get() { curl -fsS "localhost:$port$1" -o "$out/data/$2"; }
get /contexts contexts.json
get /api/me me.json
get /api/llm/config llm-config.json
get /resources/cpu resources-cpu.json
get /resources/memory resources-memory.json
get /report.json report.json
get /report.csv report.csv
ctx=$(grep -o '"context":"[^"]*"' "$out/data/contexts.json" | head -1 | cut -d'"' -f4 || true)
[ -n "$ctx" ] || { echo "no context in contexts.json" >&2; exit 1; }
nodes=$(grep -o '"node":"[^"]*"' "$out/data/report.json" | cut -d'"' -f4 | sort -u || true)
[ -n "$nodes" ] || { echo "no nodes in report.json" >&2; exit 1; }
for node in $nodes; do
  get "/api/drain?context=$ctx&node=$node" "drain-$node.json"
done

banner=$(tr -d '\n' < "$root/docs/demo/banner.html")
awk -v banner="$banner" '
  /<script src="app.js">/ { print "<script src=\"demo-shim.js\"></script>" }
  { print }
  /<body[^>]*>/ { print banner }
' "$root/web/static/index.html" > "$out/index.html"
grep -q demo-shim.js "$out/index.html" || { echo "demo shim not injected into index.html" >&2; exit 1; }
cp "$root/docs/demo/demo-shim.js" "$out/demo-shim.js"
