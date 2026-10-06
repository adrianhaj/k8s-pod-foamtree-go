---
title: Errors
weight: 5
---

| Query | Message |
| --- | --- |
| `ns:` | `ns: needs a value` |
| `qos:Cheap` | `unknown QoS class — use Guaranteed, Burstable or BestEffort` |
| `has:sidecars` | `unknown has: field — use init-containers` |
| `zone:eu` | `unknown filter — use ns:, node:, qos:, has:, audit:, health:` |
| `=frontend` | `label selector needs a key` |
| `app=` | `label selector needs a value` |
| `""` | `empty quoted value` |
| `app="my app` | `unterminated quoted value` |


Focusing the input opens a popover with the same token list; it is replaced by the error list while a token is malformed. The `×` on the right clears the query.
