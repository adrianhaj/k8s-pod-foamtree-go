---
title: Install
---

## Install

```bash
brew install adrianhaj/tap/k8sfoams   # macOS
k8sfoams                              # http://127.0.0.1:8080, uses ~/.kube/config (or $KUBECONFIG)
```

On Linux and Windows, download the archive for your OS and CPU (amd64 or arm64) from [Releases](https://github.com/adrianhaj/k8s-pod-foamtree-go/releases) and check it against `k8sfoams_<version>_checksums.txt`. The macOS binary is not signed: Homebrew clears the quarantine flag itself, but after a browser download run `xattr -d com.apple.quarantine k8sfoams` once.
