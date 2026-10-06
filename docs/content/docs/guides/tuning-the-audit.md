---
title: Tuning the audit
weight: 7
---

Move the audit thresholds, or switch rules off, so the Problems tab reports what matters on your cluster.

The rules and their markers are in the [audit rules reference](../../reference/audit-rules/); every threshold is a startup flag, listed in the [flags reference](../../reference/flags/). The UI follows the server's settings.

## Worked example: tighten the audit for prod

1. **Run with the defaults.**

   ```bash
   k8sfoams --synthetic 20x10
   ```

   **What you'll see:** the Problems tab lists `monolith` and `ratio asymmetry` findings.

2. **Lower the monolith threshold to 1%.**

   ```bash
   k8sfoams --synthetic 20x10 --audit-monolith 1
   ```

   **What you'll see:** almost every pod is a `monolith`, which shows the threshold moved.

3. **Use a realistic prod setting.**

   ```bash
   k8sfoams --audit-monolith 70 --audit-ratio 3 --audit-disable missing-limits
   ```

   **What you'll see:**
   - On a pod that reserves more than 70% of its node, the monolith tooltip says `reserves over 70% of its node — nowhere else to reschedule it`.
   - When you type `audit:missing-limits` in the query bar, it reports `audit: rule missing-limits is turned off on this server (--audit-disable)` instead of matching nothing.
   - When the cluster is clean, the Problems tab reads `No problems found. Every node is schedulable and every pod passes the audit (1 rule turned off).`

{{< callout type="warning" title="Sharp edges" >}}
A rule turned off with `--audit-disable` is not reported anywhere: the Problems tab, `audit:` queries, both reports and the assistant's tools. A clean Problems tab with rules turned off only means the remaining rules passed.
{{< /callout >}}
