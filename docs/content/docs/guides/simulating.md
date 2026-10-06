---
title: Simulating scheduling
weight: 5
---

Ask whether a pod fits, or where pods would go if a node disappeared, before you touch the cluster.

These are read-only dry runs of the scheduler's filters on the cached cluster: nothing is created, evicted or cordoned.

- **Can I fit this pod?** (**Drain simulation** tab, *Fit a pod*): CPU and memory requests, a node selector (`disk=ssd,zone=a`) and tolerations (`spot=true:NoSchedule,gpu`). Nodes that cannot take the pod are dimmed; the node overlay says why, e.g. `insufficient cpu: requires 4000m, available 1200m`. `GET /api/fit?cpu=&memory=&nodeSelector=&tolerations=`.
- **Simulate drain** (**Drain simulation** tab, *Drain a node*; the node overlay's button opens it): where each pod would land if the node were drained or lost, and which would stay Pending. DaemonSet and static pods are skipped, pods without a controller are reported as not recreated. `GET /api/drain?node=`.

```
disk=ssd,zone=a
```

**What you'll see:** pasted into the node selector of *Fit a pod*, the nodes without those labels dim, and each one's overlay gives the reason.

## Worked example: is it safe to drain worker-3?

Here `worker-3` stands in for your own node name. With `--synthetic` the nodes are named `node-0000`, `node-0001` and so on.

1. **Open the node.** Click `worker-3` on the map to open its overlay.

   **What you'll see:** the node's pods and a **Simulate drain** button.

2. **Press Simulate drain.** This opens the **Drain simulation** tab on *Drain a node*, with the node filled in.

   **What you'll see:** one row per pod that would be moved, with a summary chip counting how many are `Pending`.

3. **Read the rows.**
   - `Pending` (red): no other node can take the pod, and the reason is shown beside it. These are the pods you would lose capacity for.
   - `Not recreated` (amber): `no controller owns it`. Nothing recreates a bare pod once its node is gone.
   - `lands on`: the node the pod would move to.

   **What you'll see:** the drain is safe when there are no `Pending` and no `Not recreated` rows, and every pod `lands on` a node.

The same dry run is available over HTTP:

```bash
curl 'http://127.0.0.1:8080/api/drain?node=worker-3'
```

**What you'll see:** JSON listing the pods that would stay pending, the unmanaged pods and where the rest would land.

{{< callout type="warning" title="Sharp edges" >}}
Modelled: allocatable CPU, memory and pod count, cordons, `NoSchedule` / `NoExecute` taints, node selectors and required node affinity. Not modelled: pod (anti-)affinity, topology spread, volume zones, host ports, extended resources and preemption. The drain also ignores PodDisruptionBudgets.
{{< /callout >}}
