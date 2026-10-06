---
title: Install
weight: 1
---

Get the `k8sfoams` binary onto your machine.

```bash
brew install adrianhaj/tap/k8sfoams   # macOS
k8sfoams                              # http://127.0.0.1:8080, uses ~/.kube/config (or $KUBECONFIG)
```

On Linux and Windows, download the archive for your OS and CPU (amd64 or arm64) from [Releases](https://github.com/adrianhaj/k8s-pod-foamtree-go/releases) and check it against `k8sfoams_<version>_checksums.txt`.

The image `ghcr.io/adrianhaj/k8sfoams:<version>` (amd64 and arm64) is for running in a cluster, see [Run in a cluster](../run-in-cluster/).

## Is it working?

```bash
k8sfoams
```

Open `http://127.0.0.1:8080`.

**What you'll see:** the 2D map with one tile per node of your current kubeconfig context, each split into the pods requesting CPU on it.

{{< callout type="warning" >}}
**Sharp edges**

The macOS binary is not signed. Homebrew clears the quarantine flag itself, but after a browser download run `xattr -d com.apple.quarantine k8sfoams` once.
{{< /callout >}}
