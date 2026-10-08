---
title: Estimating cost
weight: 8
---

Put a price on every node, then read what each pod, workload and namespace costs. Every figure is an estimate, marked with `~`.

[Try it in the demo →](../../../demo/?size=cost): the demo prices its `m` and `g` nodes and leaves the `r.4xlarge` memory nodes unpriced.

## Price table

Start the server with `--prices` pointing at a CSV you keep. The first row is the header; lines starting with `#` are comments:

```csv
# USD per hour
instance_type,region,capacity_type,hourly_usd
m5.xlarge,,,0.192
m5.xlarge,eu-west-1,,0.214
m5.xlarge,eu-west-1,spot,0.07
,,,0.10
```

A blank cell matches any value, and cells are trimmed, so a CSV saved from Excel works as is. A node takes the most specific matching row: a row naming the instance type beats any row that does not, then region beats capacity type; on a tie the first row wins. The last row above is a catch-all for every other node.

Capacity type is `spot` or `on-demand`, from the node labels the map's [topology grouping](../reading-the-map/#topology) reads. GKE and self-managed EKS label only their spot nodes, so an `on-demand` row also matches a node with no capacity label.

A node no row matches stays **unpriced**: it has no cost anywhere rather than a cost of $0, and a total over pods says how many it leaves out. A malformed file stops the server at startup, naming the line.

## How a pod is charged

A pod costs its node's hourly price times its larger share of the node's CPU or memory, the way OpenCost bills requests: a pod that takes 60% of the memory pays for 60% of the node even if it uses 10% of the CPU, because nobody else can use that CPU without memory to go with it. A pod that requests nothing costs nothing. Shares are of node capacity, like the rest of the map. Because each pod pays for its larger share, pods of opposite shapes on one node can add up to more than the node's price.

Costs read per month of 730 hours.

## Idle cost

A node's **idle cost** is its price minus what its pods are charged: the money spent on capacity no request claims. Idle and requested cost add up to the node's price. A node whose pods overpay it, as pods of opposite shapes can, counts as 0 idle. Unpriced nodes are left out.

## Worked example

1. **Write a price table and start a synthetic cluster with it.**

   ```bash
   printf 'instance_type,region,capacity_type,hourly_usd\nm.4xlarge,,,0.768\nm.4xlarge,,spot,0.29\n' > prices.csv
   k8sfoams --synthetic 20x10 --prices prices.csv
   ```

   **What you'll see:** **Size by** in the toolbar offers **Cost**. Pick it: nodes and pods are sized by cost, and the summary strip shows `Cost requested` against the total price of the priced nodes. The `r.4xlarge` and `g.4xlarge` nodes have no row in this table, so they drop out of the cost map, like nodes without GPUs drop out of a GPU map.

2. **Cost of a namespace.** Type `ns:team-3` in the query bar.

   **What you'll see:** the match count adds the monthly cost of the matched pods, something like `22 / 200 pods · ~$92/mo (8 unpriced)`: 8 of them run on the `r.4xlarge` and `g.4xlarge` nodes this table leaves unpriced. Any query works the same way, so `app=svc07` prices one app.

3. **Idle cost.** Pick **Group by** → **Pool** in the toolbar.

   **What you'll see:** the summary strip shows `Cost idle` against the total price of the priced nodes, unless **Size by** is Cost, where `Cost requested` already shows it; hover it for how many nodes are unpriced. Each group label adds its own idle cost, like `~$540/mo idle`, so the pool wasting the most stands out. Sized by cost, each node's empty foam reads `idle · ~$40/mo`.

4. **Cost of a workload.** Click a pod to pin its workload.

   **What you'll see:** the workload chip adds the cost of all its replicas, with the same unpriced count.

5. **Cost per pod.** Click a node.

   **What you'll see:** the node overlay shows the node's monthly price, and each pod row its own cost.

6. **Export.** `/report.csv` adds `node_hourly_usd` and `pod_hourly_usd` columns, empty when unpriced, ready for a spreadsheet pivot by namespace. See [History and export](../history-export/).
