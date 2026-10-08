---
title: Audit rules
weight: 2
---

Every pod is checked against the rules below. For the glyph, the Problems chips and `audit:` queries, see [Finding problems](../../guides/finding-problems/#audit-and-hygiene).

| Severity | Rule | Flagged when |
| --- | --- | --- |
| <span class="pill warn">warn</span> | `missing-requests` missing requests | a regular container requests 0 CPU or 0 memory |
| <span class="pill info">info</span> | `missing-limits` no memory limit | a regular container sets no `limits.memory` |
| <span class="pill warn">warn</span> | `monolith` monolith | the pod reserves more than 80% (`--audit-monolith`) of its node's CPU or memory |
| <span class="pill info">info</span> | `ratio-asymmetry` ratio asymmetry | the pod's share of node CPU and its share of node memory differ by 4× (`--audit-ratio`) or more, and the larger share is at least 10% (`--audit-ratio-min-share`) |
| <span class="pill danger">danger</span> | `crashloop` crash loop | a container is waiting in `CrashLoopBackOff` |
| <span class="pill danger">danger</span> | `oom-killed` OOM killed | a container's last run ended `OOMKilled`, even if it has recovered since |
| <span class="pill warn">warn</span> | `throttled` throttled | the worst container was CPU-throttled in over 25% of its periods over the last 5 minutes, from Prometheus (`--prometheus-url`); off without that flag |
| <span class="pill warn">warn</span> | `image-pull` image pull | a container is waiting in `ImagePullBackOff` or `ErrImagePull` |
| <span class="pill info">info</span> | `resize-deferred` resize deferred | an in-place resize is waiting for room (`PodResizePending`, reason `Deferred`); the map still counts the larger of old and new requests, as the scheduler does |
| <span class="pill warn">warn</span> | `resize-infeasible` resize infeasible | an in-place resize can never fit the node (`PodResizePending`, reason `Infeasible`); the map counts the old, allocated requests |

The thresholds are set at startup, see [Flags](../flags/). A rule turned off with `--audit-disable` is not reported anywhere: the Problems tab, `audit:` queries, both reports and the assistant's tools. The UI knows which rules are off: an `audit:` query for one says so instead of matching nothing.

Four details are worth knowing:

- **Init containers are not audited against the best-practice rules.** They finish before the app runs, so their requests and limits say nothing about how the pod behaves once it is running. The crash signals do cover them.
- **CPU limits are not required.** Only a missing *memory* limit is flagged. A memory leak without a limit can take the whole node down; a CPU spike without a limit only gets throttled.
- **Ratio asymmetry ignores small pods.** A sidecar asking for 5% of the CPU and almost no memory has an extreme ratio, but it leaves no meaningful capacity stranded.
- **A pending resize shows what the pod wants.** The node overlay's pod row reads `wants <cores> · <memory>`; hover it for the kubelet's message.
