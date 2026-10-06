---
title: Finding problems
weight: 2
---

Spot nodes that refuse pods, capacity that no pod can use, pods that break best practice, and pods first in line for eviction.

Node health and audit findings are counted in the **Problems** tab of the bottom panel. Click a chip there to highlight those nodes or pods in 2D and 3D and dim the rest; this sets the query (see [Querying](../querying/)), and clicking the chip again clears it. Stranded capacity and QoS are not counted there; QoS is in the summary strip's Pods cell.

[Try it in the demo →](../../../demo/?q=audit%3Amissing-limits): the synthetic cluster has 33 pods without limits, so they stay highlighted and the rest dim.

## Node health

Free capacity on a node that refuses pods is not really free. A node that is cordoned, under pressure, or carrying a `NoSchedule` taint has its **idle foam hatched with diagonal warning stripes** (the plate surface in 3D), gets a warning badge next to the utilization percentage. A healthy cluster looks exactly as it did before, nothing is added.

| Marker | Reason | Meaning |
| --- | --- | --- |
| red | `cordoned` | `spec.unschedulable` is true, someone ran `kubectl cordon` |
| red | `not ready` | the `Ready` condition is `False` or `Unknown` |
| amber | `mem pressure`, `disk pressure`, `pid pressure` | the matching kubelet condition is `True` |
| blue | `tainted` | at least one taint has effect `NoSchedule` or `NoExecute` |

A warning's chip sets the query `health:<warning>`. The glyphs in the Nodes cell of the summary strip toggle the same query.

Click a node to open the focus overlay: a **Scheduling** section spells out every reason and lists each taint as `key=value` with its effect. Worst reason wins the overlay's status pill: a cordoned node under memory pressure reads as `SCHEDULING-DISABLED`, because that is what actually keeps pods off it.

```
health:cordoned
```

**What you'll see:** the cordoned nodes and their pods highlighted, every other node dimmed.

{{< callout type="warning" >}}
**Sharp edges**

- **`PreferNoSchedule` never marks a node.** It is a soft hint the scheduler is free to ignore, so it is listed in the focus overlay but does not stripe.
- **The cordon taint is folded into `cordoned`.** Kubernetes adds `node.kubernetes.io/unschedulable:NoSchedule` itself when you cordon; reporting it as a taint too would mark the same node twice for one fact, so it is dropped from the taint list.
{{< /callout >}}

## Stranded capacity

Free CPU with no free memory beside it, or the reverse, cannot hold a real pod. k8sfoams judges free capacity against the cluster's **median pod shape**: the median memory-to-CPU ratio of every pod that requests both.

- **Group labels** (2D, with **Group by** set) add `… stranded` when a group's nodes hold CPU or memory that no pod of that shape can use.
- **Node overlay**: a **Stranded capacity** section says how much and on which axis.
- **Summary strip**: the Nodes cell reads `fits X c · Y GiB`, the largest pod of the median shape that still fits on a node with no warnings, or `full`.

On a node with `c` free millicores, `m` free MiB and shape `r` (MiB per millicore), stranded CPU is `c − min(c, m / r)` and stranded memory is `m − min(m, c · r)`. Only one is ever non-zero.

**What you'll see:** a `… stranded` suffix on group labels, and a **Stranded capacity** section in a node's overlay.

{{< callout type="warning" >}}
**Sharp edges**

Like the empty foam, stranded capacity is measured against capacity, not allocatable, so it overstates by the node's system reservations. Without any pod that requests both CPU and memory, there is no shape and nothing is shown.
{{< /callout >}}

## Audit and hygiene

Every pod is checked against the audit rules. A pod that breaks one gets a **small warning glyph in the top-right corner** of its box (hover it for the reasons). A rule's chip sets the query `audit:<rule>`. A clean cluster reads `No problems found`.

The rules, their thresholds and the details worth knowing are in the [audit rules reference](../../reference/audit-rules/); to move a threshold or switch a rule off, see [Tuning the audit](../tuning-the-audit/).

```
audit:crashloop
```

**What you'll see:** pods with a container waiting in `CrashLoopBackOff` highlighted, everything else dimmed.

## QoS and eviction risk

Under memory pressure the kubelet evicts pods by QoS class. Set **Color by → QoS** in the toolbar to color every pod box (2D) and cube (3D) by its class. Node cards and plates turn a neutral slate, so only the pods carry color.

| Color | Class | Eviction order |
| --- | --- | --- |
| red | `BestEffort` | first, no container sets any request or limit |
| amber | `Burstable` | second, requests are set but lower than limits |
| green | `Guaranteed` | last, every container's requests equal its limits |

The Pods cell of the summary strip shows pods per class as a segmented bar, riskiest first, in every color mode. Hover a segment for its count. Click a segment to set the query `qos:<Class>`.

```
qos:BestEffort
```

**What you'll see:** the `BestEffort` pods highlighted, the pods the kubelet evicts first.

{{< callout type="warning" >}}
**Sharp edges**

A `BestEffort` pod requests nothing, so in 2D it takes no room until it is highlighted, then it shows as a thin sliver. The class is read from the pod's `status.qosClass`, which the API server sets when the pod is created. It is not recomputed from requests and limits. A pod with no reported class renders neutral and is not counted.
{{< /callout >}}

## Worked example: why is this node full?

A node looks full but its pods look small. Here `worker-3` stands in for your own node name.

1. **Open the Problems tab** of the bottom panel.

   **What you'll see:** counts of nodes per warning and pods per audit rule, or `No problems found` on a clean cluster.

2. **Query the node.** Type this in the query bar:

   ```
   node:worker-3
   ```

   **What you'll see:** the pods on that node glow and everything else dims, with a counter in the input reading `N / M pods`.

3. **Read the stranded capacity.** Click the node to open its overlay and read the **Stranded capacity** section.

   **What you'll see:** how much capacity is stranded and on which axis, CPU or memory. Free CPU with no memory beside it, or the reverse, cannot hold a pod of the cluster's median shape.

4. **Narrow to lopsided pods.** Add the `ratio-asymmetry` audit rule, which flags lopsided pods (see the [audit rules](../../reference/audit-rules/)):

   ```
   audit:ratio-asymmetry node:worker-3
   ```

   **What you'll see:** only the pods on `worker-3` that break the `ratio asymmetry` rule stay highlighted.
