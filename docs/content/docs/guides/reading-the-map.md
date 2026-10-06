---
title: Reading the map
weight: 1
---

Understand what the 2D map and the 3D view show, and how to group and recolour them.

## How the map is built

k8sfoams reads kubeconfig contexts as described in [Run locally](../../getting-started/run-locally/). From the watch cache it builds the map in four steps:

1. **Normalize.** CPU becomes millicores and memory becomes decimal kB, using Kubernetes' own quantity parser. A pod's **effective request** is the scheduler's formula (`k8s.io/component-helpers` `PodRequests`): regular containers and native sidecars (init containers with `restartPolicy: Always`) are summed, plain init containers run one at a time so the largest of them is maxed against that sum, and pod overhead and pod-level resources are added. During a pending in-place resize the scheduler counts the larger of the spec and the kubelet's allocated/actuated resources, or only the allocated/actuated ones when the resize is Infeasible.
2. **Nest.** The result is nested node → pod → container, with a synthetic `empty` child per node for free capacity.
3. **Serve.** The nested tree is served as JSON.
4. **Render.** A React single-page app, compiled at build time by `go tool esbuild`, is embedded in the binary together with React's production build, so nothing loads from a CDN. It fetches CPU and memory in parallel, merges them, and renders. The view auto-refreshes every 60 seconds by default.

## 2D map

![k8sfoams 2D treemap view](../../../img/k8s-foam-tree.png)

A squarified treemap. Each node is a square box, each pod is a foam inside it. A pod with more than one container is split into sub-foams. The empty foam is unused (free) capacity on that node. Pick **CPU** or **Memory** with **Size by** in the toolbar; **Group by** boxes the nodes by zone, region, pool, instance type or capacity type, see [Topology](#topology). Nodes that offer GPUs, ephemeral storage or hugepages add them to that control: the map then sizes nodes and pods by that resource, nodes without it drop out, and the node overlay shows how much of each is requested.

**What you'll see:** one box per node, pods as foams inside it, and a foam for the free capacity left on that node.

## 3D view

![k8sfoams 3D cube view](../../../img/k8s-foam-tree-3d.png)

A WebGL (Three.js) scene: one plate per node, one cube per pod. A cube encodes both resources at once:

- **width × depth** (footprint) → CPU request
- **height** → memory request
- **color** → namespace, or the pod's QoS class or audit findings with **Color by** (QoS, Problems)
- **translucent shell** → the pod's limits, grown only on the axes that have one (footprint from the CPU limit, height from the memory limit). A pod without a limit on an axis has no ceiling to draw there.

Both dimensions are square-root scaled, so a 10× larger pod is not 10× wider. Because a cube already shows both resources, the **Size by** control is replaced in 3D by a **Zoom** slider. Drag to orbit, scroll to zoom; hover a pod for its requests and limits, click it to pin its workload, click a plate to open the node.

**Group by** splits the plates into framed blocks, see [Topology](#topology). On refresh, new pods grow in and removed ones shrink out; the camera stays where you left it unless nodes join or leave.

Switch views with the Map and 3D buttons on the rail at the map's left edge. Frames render only when something changes, so an idle dashboard costs nothing.

**What you'll see:** a plate per node with a cube per pod; hovering a cube shows its requests and limits.

{{< callout type="warning" title="Sharp edges" >}}
Without WebGL, a notice points to the 2D map.
{{< /callout >}}

## Topology

**Group by** in the toolbar (None, Zone, Region, Pool, Type or Capacity) frames the nodes of each availability zone, region, node pool, instance type or capacity type (spot vs on-demand): in the 2D map as a box of node cards sized by the group's total capacity, in the 3D scene as a block of plates on a shared floor. Each group's label reads its name, node count and the requested share of its capacity (the selected resource in 2D, the tighter one in 3D), so a zone or pool running hotter than its siblings stands out.

| Group by | Node label |
| --- | --- |
| Zone | `topology.kubernetes.io/zone` |
| Region | `topology.kubernetes.io/region` |
| Pool | first of `karpenter.sh/nodepool`, `eks.amazonaws.com/nodegroup`, `cloud.google.com/gke-nodepool`, `kubernetes.azure.com/agentpool` |
| Type | `node.kubernetes.io/instance-type` |
| Capacity | `spot` or `on-demand`, from the first of `karpenter.sh/capacity-type`, `eks.amazonaws.com/capacityType` (`SPOT`/`ON_DEMAND`), `cloud.google.com/gke-spot=true` or `cloud.google.com/gke-provisioning=spot`, `kubernetes.azure.com/scalesetpriority` (`spot`/`regular`) |

A node without the label goes to its own group, `no zone`, `no pool`, and so on.

**What you'll see:** framed groups whose labels read name, node count and requested share of capacity.

## Themes

The dashboard follows the OS light or dark setting by default. **Settings → Theme** on the rail (System, Light, Dark) overrides it, and the choice is remembered per browser.

## Controls

- **Memory unit**: MiB, GiB (default), or TiB, under Settings on the rail.
- **Size by**: CPU or Memory, in the toolbar (in 3D, a **Zoom** slider takes its place).
- **Color by**: in the toolbar. *Namespace* (default) colors each pod by its namespace; *QoS* by its QoS class (see [QoS & eviction risk](../finding-problems/)); *Problems* by the severity of its audit findings. A legend sits beside it.
- **Context**: the context picker in the top bar lists every context from your kubeconfig, in kubeconfig order, the active one checked, tagged by provider. **Switching only changes the context inside the k8sfoams web server — your ~/.kube/config file is never modified.**
- **Refresh**: the Auto-refresh menu in the top bar (Off, 15 s … 10 min), plus a *Refresh now* button beside it.
- **Filter**: the query bar in the top bar highlights matching pods and dims the rest — nothing is removed from the view. See [Filtering](../querying/) for the full grammar.
- **Focus**: click a node to open an overlay listing its pods with per-pod CPU/memory and container breakdown, plus its instance type, zone and pool.
- **Pending**: the Pending tab of the bottom panel lists pods no node has taken, with the scheduler's reason, e.g. `0/12 nodes are available: 12 Insufficient memory.`
- **Share**: the address bar always links to the view on screen. Context, 2D or 3D, Size by, Group by, Color by and the query are kept in the URL (`/?view=3d&group=zone&q=ns%3Apayments`), so a pasted link opens the same picture, also after an OIDC sign-in. With more than one kubeconfig context the URL also names the context, so the link opens the same cluster. Theme, memory unit and panel state stay per browser.
