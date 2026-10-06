---
title: HTTP API
weight: 4
---

| Route | Returns |
| --- | --- |
| `GET /` | the dashboard |
| `GET /healthcheck` | `{"status": "ok"}` |
| `GET /resources/cpu`, `GET /resources/memory` | treemap JSON; optional `?context=<name>`. CPU in millicores, memory in decimal kB. A top-level `pending` list holds every pod with no node yet, as `{namespace, name, reason, message}` from its `PodScheduled=False` condition (`""` before the scheduler has tried). A top-level `audit` holds the server's `monolithShare` (a fraction) and the `disabled` rules, so the UI's copy and `audit:` queries follow the [audit flags](../flags/). Each node group also carries `unschedulable`, `taints`, `conditions` and a render-ready `warnings` list — see [Node health](../../guides/finding-problems/). Each pod group carries a `findings` list — see [Audit & hygiene](../audit-rules/) — plus `phase` and `statuses` (per container: `name`, `ready`, `restarts`, and when set `waiting`, `lastExitReason`, `lastExitCode`). Pod groups carry `limit` on that axis (`null` = no ceiling); node groups carry `zone`, `region`, `instanceType`, `pool` and `capacityType` (`"spot"` or `"on-demand"`) — `""` when no label says. Node, pod and container entries carry `extended`: every other non-zero resource (`nvidia.com/gpu`, `ephemeral-storage`, `hugepages-2Mi`, …) in its base unit, bytes or a device count, from node allocatable (CPU and memory stay on capacity) and the effective request; omitted when there is none. |
| `GET /contexts` | `[{"context": "...", "active": true}]` |
| `GET /api/logs` | one container's logs as plain text. `namespace` and `pod` are required; optional `container`, `tail` (1–5000, default 500), `previous=1` for the run before the last restart, and `context`. At most 1 MiB. 404 when the pod is gone, 400 when there is no previous run. |
| `GET /api/llm/config` | `{"server", "url", "model", "maxTokens", "toolTokens"}`, where `toolTokens` estimates the lookup schema sent with every question; never the key |
| `POST /api/llm/chat` | takes JSON `{url?, model?, context, budget, messages}` and an optional `X-LLM-Key` header; a question is limited to 8 rounds (the last offers no lookups), 16 lookups and 64 KiB of lookup output; streams `text/event-stream` events `delta`, `tool` (one per lookup the model makes), `notice`, `error` and `done` (with `usage` and `masked`, the count of values masked before sending) |
| `GET /api/me` | `{"auth": "none"}`, or `{"auth": "oidc", "email": "...", "name": "..."}` for the signed-in user |
| `GET /auth/login`, `GET /auth/callback`, `POST /auth/logout` | OIDC sign-in and sign-out (only with `--auth=oidc`) |

An unknown `context` returns 400; an unreachable cluster or rejected credentials return 503 with the error text.
