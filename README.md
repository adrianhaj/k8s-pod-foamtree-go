# k8s-pod-foamtree

<p align="center">
  <img src="logo.png" alt="k8sfoams logo">
</p>

> Go implementation of [mmpyro/k8s-pod-foamtree](https://github.com/mmpyro/k8s-pod-foamtree).

**k8sfoams** is a read-only dashboard that answers one question: *where is my cluster's requested CPU and memory actually going, and how much room is left on each node?*

It visualizes **resource requests** — what the scheduler reserves — not live usage. That makes it a tool for spotting over-requesting pods and idle headroom, not a performance monitor. It is one static Go binary with the UI embedded. Run it on your laptop against *~/.kube/config* (or `$KUBECONFIG`), or inside the cluster behind built-in OIDC sign-in. It needs no metrics-server.
![k8sfoams 2D treemap view](k8s-foam-tree.png)

## Quick start

```bash
brew install adrianhaj/tap/k8sfoams
k8sfoams                       # http://127.0.0.1:8080
k8sfoams --synthetic 20x10     # no cluster needed
```

## Documentation

Full docs: <https://adrianhaj.github.io/k8s-pod-foamtree-go/>

- [Getting started](https://adrianhaj.github.io/k8s-pod-foamtree-go/docs/getting-started/install/)
- [Guides](https://adrianhaj.github.io/k8s-pod-foamtree-go/docs/guides/)
- [Flags](https://adrianhaj.github.io/k8s-pod-foamtree-go/docs/reference/flags/)
- [HTTP API](https://adrianhaj.github.io/k8s-pod-foamtree-go/docs/reference/http-api/)

## Development

Requires Go 1.27.1. No Node: the JSX is compiled by `go tool esbuild` and React is vendored in `web/static/vendor/`.

```bash
make test lint
make clean
make image
make run ARGS="--synthetic 400x50"
make build                    # bin/k8sfoams
```

Releases: an admin pushes a `v*` tag (`git tag v1.0.0 && git push origin v1.0.0`; the `protect-release-tags` ruleset blocks everyone else). `.github/workflows/release.yaml` then runs GoReleaser (`.goreleaser.yaml`), which attaches the archives and their checksums to a GitHub Release and pushes the Homebrew cask to [adrianhaj/homebrew-tap](https://github.com/adrianhaj/homebrew-tap), and pushes the image as `:<tag>` and `:latest`. Pre-release tags such as `v1.1.0-rc.1` skip the cask and `:latest`. The tap token is a fine-grained PAT (Contents read/write on `homebrew-tap` only), stored as the `HOMEBREW_TAP_TOKEN` secret of the `release` environment, which only `v*` tags may deploy to.
