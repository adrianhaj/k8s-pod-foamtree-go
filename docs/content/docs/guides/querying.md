---
title: Querying
weight: 3
---

Narrow the map to the pods and nodes you care about with the query bar.

## Filtering

The query bar in the top bar is a **highlighter, not a filter of last resort**: matching pods glow, everything else dims. No pod, node or box ever leaves the layout, so the shape of the cluster stays comparable while you narrow down. Once the query is non-empty and valid, a live counter inside the input reads `N / M pods` (and turns red at `0`).

Type whitespace-separated tokens. **All tokens are ANDed**: a pod must satisfy every one of them.

```
ns:kube-system qos:Burstable app=frontend
```

**What you'll see:** only `Burstable` pods in `kube-system` labelled `app=frontend` glow.

An empty query matches everything. A query that contains a malformed token is **inert**: nothing dims, and the offending tokens are listed under the bar with the reason. Half-typing `ns:` can never blank the view.

The tokens are listed in the [token reference](../../reference/query-tokens/).

## Examples

Every pod named like `nginx`, anywhere:

```
nginx
```

**What you'll see:** every pod whose name contains `nginx` glows.

Pods in `kube-system` that run init containers:

```
ns:kube-system has:init-containers
```

**What you'll see:** only `kube-system` pods that declare at least one init container glow.

Everything the scheduler can evict first, on the worker pool:

```
qos:BestEffort node:worker-*
```

**What you'll see:** `BestEffort` pods on nodes whose name starts with `worker-`.

Frontend pods that are **not** in production, named like `api`:

```
app=frontend env!=prod api
```

**What you'll see:** pods labelled `app=frontend` whose `env` label differs from `prod` or is missing, with `api` in the name.

One specific node. Globs are anchored, so dots are literal, not wildcards:

```
node:ip-10-0-1-5.ec2.internal
```

**What you'll see:** the pods on exactly that node.

All nodes in an AZ suffix, plus a namespace:

```
node:*-eu-west-1a ns:payments
```

**What you'll see:** `payments` pods on nodes whose name ends in `-eu-west-1a`.

Guaranteed pods carrying a label value with a space:

```
qos:Guaranteed app="my app"
```

**What you'll see:** `Guaranteed` pods whose `app` label is `my app`.

Match a pod name that *looks* like a filter token. Leading quotes make the whole token literal text:

```
"web:1"
```

**What you'll see:** pods whose name contains `web:1`. Without the quotes, `web:1` is read as an unknown filter prefix and reported as an error.

{{< callout type="warning" >}}
**Sharp edges**

- **`!=` wins over `=`.** `env!=prod` is one inequality, never `env!` equals `prod`.
- **A filter prefix must be a bare word before `:`.** `app=ns:x` is a label selector for key `app`, value `ns:x`, not a namespace filter.
- **Only `node:` and `health:` can dim a node.** Node plates and boxes stay in the layout either way; pod-level terms dim pods, never their node.
- **A missing label matches `!=`.** `env!=prod` highlights pods with `env: staging` *and* pods with no `env` label at all, the Kubernetes selector semantics.
- **Every problem is reported at once.** The parser never stops on the first bad token, so a three-error query lists three errors. The messages are listed in [Errors](../../reference/errors/).
- **The popover and the clear button.** Focusing the input opens a popover with the same token list; it is replaced by the error list while a token is malformed. The `×` on the right clears the query.
{{< /callout >}}
