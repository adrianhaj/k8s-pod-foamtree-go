---
title: Run locally
weight: 2
---

Run k8sfoams on your laptop against the clusters in your kubeconfig, or against made-up data.

## Kubeconfig and contexts

k8sfoams reads *~/.kube/config*, or `$KUBECONFIG` when set. It keeps a watch cache of nodes (`status.capacity`) and all non-terminated pods per kubeconfig context, started on the first request for that context. A refresh reads memory and never lists the API server. Pods in `Succeeded` or `Failed` are excluded, since they still report requests via the API but no longer reserve anything.

```bash
k8sfoams
```

**What you'll see:** the dashboard on `http://127.0.0.1:8080` for your current context.

The context picker in the top bar lists every context from your kubeconfig, in kubeconfig order, the active one checked and tagged by provider. Switching only changes the context inside the k8sfoams web server. Your *~/.kube/config* file is never modified.

## Another port

```bash
k8sfoams --port 9090
```

**What you'll see:** the same dashboard on `http://127.0.0.1:9090`. See the [flags](../../reference/flags/).

## Try it with no cluster

```bash
k8sfoams --synthetic 20x10
```

**What you'll see:** a made-up cluster of 20 nodes with 10 pods each. It also includes GPU, ephemeral-storage and hugepages nodes and three unschedulable pods, so the Pending tab has something to show. No cluster or kubeconfig is needed.
