---
title: Run in a cluster
weight: 3
---

Run k8sfoams as a Deployment, with sign-in through your OpenID Connect provider.

## Try it on kind

```bash
make kind-up deploy-dev port-forward   # local kind cluster, auth off, http://localhost:8080
make kind-down
```

**What you'll see:** a local kind cluster, then the dashboard at `http://localhost:8080` with auth off.

`make kind-up` keeps the kind cluster's credentials in `./kind.kubeconfig` (gitignored) and never touches *~/.kube/config*; every kind target passes that file explicitly. To run the binary against the same cluster: `KUBECONFIG="$PWD/kind.kubeconfig" make run`.

## Run it in-cluster behind OIDC

For a real cluster, write an overlay on `deploy/base` that sets your image (pin a release, e.g. `newTag: v1.0.0`), OIDC issuer, client id, redirect URL, allowed groups/emails and Ingress host, then create the secret and apply. The steps below use a checkout of the repository.

**1. Apply the base.**

```bash
kubectl apply -k deploy/base
```

**What you'll see:** the `k8sfoams` namespace, service account, read-only ClusterRole and binding, Deployment, Service and Ingress created. The pod does not start until the Secret in step 2 exists.

**2. Create the Secret.** The Deployment reads `K8SFOAMS_OIDC_CLIENT_SECRET` and `K8SFOAMS_SESSION_KEY` from it.

```bash
kubectl -n k8sfoams create secret generic k8sfoams-oidc \
  --from-literal=client-secret=<from your IdP> \
  --from-literal=session-key="$(openssl rand -base64 32)"
```

**What you'll see:** `secret/k8sfoams-oidc created`.

**3. Patch the OIDC arguments.** Put this in `my-overlay/kustomization.yaml`, with your own issuer, client id, redirect URL and group. See the [flags](../../reference/flags/) for `--auth`, `--oidc-issuer`, `--oidc-client-id`, `--oidc-redirect-url` and `--oidc-allowed-groups`.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../deploy/base
images:
  - name: ghcr.io/adrianhaj/k8sfoams
    newTag: v1.0.0
patches:
  - target: {kind: Deployment, name: k8sfoams}
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/args
        value:
          - --in-cluster
          - --host=0.0.0.0
          - --auth=oidc
          - --oidc-issuer=https://issuer.example.com
          - --oidc-client-id=k8sfoams
          - --oidc-redirect-url=https://k8sfoams.mycompany.com/auth/callback
          - --oidc-allowed-groups=k8sfoams-viewers
```

**What you'll see:** nothing yet; this is a file. `kubectl kustomize my-overlay` prints the Deployment with these arguments.

**4. Set the ingress host and apply.** Add a second patch under `patches:` in the same file. The base uses `k8sfoams.example.com` in the TLS hosts and the rule.

```yaml
  - target: {kind: Ingress, name: k8sfoams}
    patch: |-
      - op: replace
        path: /spec/tls/0/hosts/0
        value: k8sfoams.mycompany.com
      - op: replace
        path: /spec/rules/0/host
        value: k8sfoams.mycompany.com
```

```bash
kubectl apply -k my-overlay
```

**What you'll see:** the Deployment and Ingress configured. Opening `https://k8sfoams.mycompany.com` sends you to your identity provider, then back to the dashboard.

Register `https://<host>/auth/callback` as the redirect URI in your IdP.

{{< callout type="warning" >}}
**Sharp edges**

The service account can only `get`/`list`/`watch` nodes and pods, and `get` pod logs. Every allowed user sees the whole cluster through it, including every pod's logs, which often contain secrets.

On Microsoft Entra ID, prefer `--oidc-allowed-groups` or a single-tenant issuer: Entra omits `email_verified`, and an email glob would trust an unverified address.
{{< /callout >}}

## Images and releases

`make image` builds `ghcr.io/adrianhaj/k8sfoams:<git describe>` locally; `make image-push IMAGE=<registry>/k8sfoams` pushes amd64 and arm64. CI (`.github/workflows/go.yaml`) lints, tests and builds both on every PR and on `main`.

Releases: an admin pushes a `v*` tag (`git tag v1.0.0 && git push origin v1.0.0`). `.github/workflows/release.yaml` then runs GoReleaser, which attaches the archives and their checksums to a GitHub Release, pushes the Homebrew cask, and pushes the image as `:<tag>` and `:latest`. Pre-release tags such as `v1.1.0-rc.1` skip the cask and `:latest`.
