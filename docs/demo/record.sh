#!/usr/bin/env bash
# Records the console's API from --synthetic so /demo/ runs with no backend.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
out="$root/docs/static/demo"
port=${DEMO_PORT:-18090}
rm -rf "$out" && mkdir -p "$out/data"
cp -R "$root/web/static/." "$out/"

"$root/bin/k8sfoams" --synthetic 20x10 --port "$port" >/dev/null 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true' EXIT
for _ in $(seq 50); do curl -fs "localhost:$port/healthcheck" >/dev/null && break; sleep 0.2; done

get() { curl -fsS "localhost:$port$1" -o "$out/data/$2"; }
get /contexts contexts.json
get /api/me me.json
get /api/llm/config llm-config.json
get /resources/cpu resources-cpu.json
get /resources/memory resources-memory.json
get /report.json report.json
get /report.csv report.csv
ctx=$(grep -o '"context":"[^"]*"' "$out/data/contexts.json" | head -1 | cut -d'"' -f4)
for node in $(grep -o '"node":"[^"]*"' "$out/data/report.json" | cut -d'"' -f4 | sort -u); do
  get "/api/drain?context=$ctx&node=$node" "drain-$node.json"
done

banner=$(tr -d '\n' < "$root/docs/demo/banner.html")
awk -v banner="$banner" '
  /<script src="app.js">/ { print "<script src=\"demo-shim.js\"></script>" }
  { print }
  /<body[^>]*>/ { print banner }
' "$root/web/static/index.html" > "$out/index.html"
cp "$root/docs/demo/demo-shim.js" "$out/demo-shim.js"
