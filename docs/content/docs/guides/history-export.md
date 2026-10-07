---
title: History and export
weight: 4
---

Go back to how the cluster looked a few minutes ago, compare it with now, and take the map or the pod list out of the browser.

## History

The browser records every refresh that changed something: in memory, per tab, up to 64 MB gzipped (about 300 refreshes of a 5 000-pod cluster); a reload starts over. Open the **Changes** tab of the bottom panel and drag the **History** slider to go back, **Play** replays the recording at one snapshot a second, **Live** returns. The auto-refresh keeps recording while you look back. In 3D, pods that appear or disappear between snapshots grow in and shrink out.

**Compare** takes the snapshot on screen as a baseline; from then on the **Changes** tab lists pods added (green) and removed (red), workloads whose per-pod requests changed (amber) and per-node deltas, and the map highlights the added and resized pods (a typed query takes precedence). The baseline survives a context switch, so it also compares two contexts: record one, press Compare, switch to the other.

**What you'll see:** after pressing Compare and waiting for a rollout, added pods in green and removed pods in red in the **Changes** tab, with the new pods highlighted on the map.

## Export

The download button in the top bar saves the 2D map as SVG or PNG, the 3D view as PNG, either as PDF through the print dialog (pick *Save as PDF*), and downloads both reports.

`/report.csv` and `/report.json` list every pod with its node, requests, limits and findings (CPU in millicores, memory in bytes), plus estimated [cost](../cost/) per hour when the server runs with `--prices`. Pending pods are listed last, with the node columns empty:

```bash
curl -o pods.csv 'http://127.0.0.1:8080/report.csv?context=kind-k8sfoams'
```

**What you'll see:** a `pods.csv` file with one row per pod, and the Pending pods at the bottom with empty node columns.
