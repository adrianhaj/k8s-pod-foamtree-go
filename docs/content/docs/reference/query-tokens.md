---
title: Query tokens
weight: 3
---

For worked examples, see [Querying](../../guides/querying/).

| Token | Matches | Notes |
| --- | --- | --- |
| `ns:<name>` | pod namespace, exact | case-insensitive (`ns:Kube-System` works) |
| `node:<glob>` | node the pod is scheduled on | `*` is the only wildcard; anchored (whole name must match); case-insensitive |
| `qos:<class>` | [QoS class](../../guides/finding-problems/#qos-and-eviction-risk): `Guaranteed`, `Burstable`, `BestEffort` | case-insensitive; anything else is an error |
| `has:init-containers` | pods declaring at least one init container | currently the only `has:` field |
| `audit:<rule>` | pods breaking an [audit rule](../audit-rules/) | `missing-requests`, `missing-limits`, `monolith`, `ratio-asymmetry`, `crashloop`, `oom-killed`, `image-pull`, `resize-deferred`, `resize-infeasible` |
| `health:<warning>` | nodes carrying a [health warning](../../guides/finding-problems/), and every pod on them | `cordoned`, `not-ready`, `memory-pressure`, `disk-pressure`, `pid-pressure`, `tainted` |
| `key=value` | pod label equals value | key and value are **case-sensitive** (Kubernetes labels are) |
| `key!=value` | pod label differs from value | a **missing** label counts as unequal, so it matches too |
| `text` | pod name contains `text` | case-insensitive substring |
| `"quoted text"` | pod name contains `quoted text` | quotes force literal text — the grammar is skipped |
