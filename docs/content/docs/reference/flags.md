---
title: Flags
weight: 1
---

| Flag | Default | |
| --- | --- | --- |
| `--host` | `127.0.0.1` | listen address |
| `--port` | `8080` | listen port |
| `--in-cluster` | off | use the pod's service account; offers one context, `in-cluster` |
| `--auth` | `none` | `none` or `oidc`; with `none` on a loopback address the server only answers requests addressed to `localhost` or a loopback IP, which blocks DNS-rebinding pages from reading cluster data |
| `--allow-unauthenticated` | off | required for `--auth=none` on a non-loopback host |
| `--oidc-issuer` | | required with `--auth=oidc` |
| `--oidc-client-id` | | required with `--auth=oidc` |
| `--oidc-redirect-url` | | required with `--auth=oidc` |
| `--oidc-allowed-emails` | | comma-separated; globs like `*@example.com`. Only verified emails match (`email_verified`, or Entra's optional `xms_edov` claim); otherwise use groups |
| `--oidc-allowed-groups` | | comma-separated; at least one allow list is required |
| `--oidc-groups-claim` | `groups` | ID-token claim with group names |
| `--oidc-scopes` | `openid,email,profile` | add `groups` for Dex/Keycloak |
| `--version` | | print the version (`git describe` of the build) and exit |
| `-v` | | print the version (`git describe` of the build) and exit |
| `--llm-url` | | the assistant connection shared by every viewer: an OpenAI-compatible `http` or `https` base URL, without credentials, query or fragment; required together with `--llm-model` |
| `--llm-model` | | the assistant connection shared by every viewer: the model name; required together with `--llm-url` |
| `--llm-api-key-file` | | file holding the server's API key, e.g. a mounted Secret; re-read on every request. Used only with `--llm-url` |
| `--llm-allowed-hosts` | | comma-separated globs of hosts viewers may send their own key to; empty means only the `--llm-url` host. Ignored on a loopback run without auth, where any URL is allowed |
| `--llm-max-tokens-per-question` | `50000` | token cap for one question; `0` means none. Server connection only |
| `--synthetic` | | serve a made-up cluster, e.g. `100x50` (nodes × pods per node), for UI work and scale tests; includes GPU, ephemeral-storage and hugepages nodes and three unschedulable pods |
| `--audit-monolith` | `80` | percent of a node's CPU or memory above which a pod is a `monolith`; (0, 100] |
| `--audit-ratio` | `4` | `ratio asymmetry` factor (finite, above 1) |
| `--audit-ratio-min-share` | `10` | the larger share in percent, [0, 100], under which a pod is skipped |
| `--audit-disable` | | comma-separated rules to turn off: `missing-requests`, `missing-limits`, `monolith`, `ratio-asymmetry`, `crashloop`, `oom-killed`, `image-pull`, `resize-deferred`, `resize-infeasible` |
| `--metrics-addr` | | serve [Prometheus metrics](../metrics/) at `/metrics` on this `host:port`, e.g. `:9090`, outside OIDC; unset means no metrics listener. The port must be 1–65535 and differ from `--port` |

Secrets come from the environment only: `K8SFOAMS_OIDC_CLIENT_SECRET`, `K8SFOAMS_SESSION_KEY` (32 bytes, base64; unset means a random key, so sessions end on restart), and `K8SFOAMS_LLM_API_KEY`, or `--llm-api-key-file` for a mounted Secret.
