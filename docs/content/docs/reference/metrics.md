---
title: Prometheus metrics
weight: 6
---

With `--metrics-addr`, k8sfoams serves `GET /metrics` in the Prometheus text format on that address, so you can alert on what the map shows with the stack you already run. Without the flag there is no metrics listener and no `/metrics` route.

The metrics listener serves only `/metrics`: no dashboard, no API and no OIDC sign-in. Keep its port off your Ingress. Startup fails on a malformed address, a port outside 1–65535, the dashboard's own `--port`, or a port already in use.

Every scrape reads the watch cache of each context that has already started: the default context, which k8sfoams starts when metrics are on and retries every 30 seconds until it syncs, and any other context a viewer has opened. A scrape never starts a watch. Every sample carries a `context` label.

| Metric | Labels | |
| --- | --- | --- |
| `k8sfoams_context_up` | `context` | `1` when the context's watch cache answered this scrape; `0` when it failed, or for the default context before it has started. A context that is down has no other samples |
| `k8sfoams_capacity_cpu_cores` | `context`, `pool`, `zone` | node CPU capacity, summed |
| `k8sfoams_headroom_cpu_cores` | `context`, `pool`, `zone` | CPU capacity no pod requests: the empty foam |
| `k8sfoams_capacity_memory_bytes` | `context`, `pool`, `zone` | node memory capacity, summed |
| `k8sfoams_headroom_memory_bytes` | `context`, `pool`, `zone` | memory capacity no pod requests |
| `k8sfoams_requested_ratio` | `context`, `pool`, `zone`, `resource` (`cpu`, `memory`) | requested share of capacity, `0.8` = 80% |
| `k8sfoams_audit_findings` | `context`, `rule` | pods with each [audit finding](../audit-rules/); rules turned off with `--audit-disable` are left out |
| `k8sfoams_node_warnings` | `context`, `warning` | nodes with each [warning](../../guides/finding-problems/#node-health): `cordoned`, `not-ready`, `memory-pressure`, `disk-pressure`, `pid-pressure`, `tainted` |
| `k8sfoams_pending_pods` | `context` | pods no node has taken yet |

`pool` and `zone` come from the same node labels as the map's topology grouping, and are empty when no label says. Like the map, capacity is node capacity, not allocatable, and headroom counts cordoned and not-ready nodes. Join with `k8sfoams_node_warnings` if an alert must not. Every rule and warning reports `0` rather than going missing, so an alert on `> 0` sees the drop back to zero.

## Example alerts

```yaml
groups:
  - name: k8sfoams
    rules:
      - alert: K8sfoamsContextDown
        expr: k8sfoams_context_up == 0
        for: 5m
      - alert: PoolNearlyFull
        expr: k8sfoams_requested_ratio > 0.9
        for: 15m
      - alert: PodsPending
        expr: k8sfoams_pending_pods > 0
        for: 10m
      - alert: PodsOOMKilled
        expr: k8sfoams_audit_findings{rule="oom-killed"} > 0
```

## In the cluster

Add the metrics component to your overlay. It adds `--metrics-addr=:9090`, a `metrics` container port, the `prometheus.io/*` pod annotations and a `k8sfoams-metrics` Service. `deploy/base` stays without metrics.

```yaml
components:
  - ../deploy/components/metrics
```

{{< callout type="warning" >}}
An overlay patch that replaces the container's `args` runs after the component and drops its `--metrics-addr=:9090`: the Service and annotations stay, but nothing answers on 9090. List `--metrics-addr=:9090` in your own `args` too.
{{< /callout >}}
