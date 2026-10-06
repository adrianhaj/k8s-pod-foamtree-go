---
title: Logs and the assistant
weight: 6
---

Read a container's logs without leaving the map, and ask a model about what you see.

## Logs

The **Logs** tab shows one container's logs. Pick a pod by `namespace/name`, then the container, the **Current** or **Previous** run (the one before the last restart, where a crash shows up) and how many lines. **Highlight text** marks matching lines without hiding the others. Open it from the **Logs** action on a selected workload's chip, or on any pod row in a node's overlay; the map lights that pod. Logs load when you open or reload them, never in the background. They need `get` on `pods/log`, which the base RBAC grants.

**What you'll see:** the log lines of the chosen container, with lines matching **Highlight text** marked and the pod lit on the map.

## Assistant

The **Assistant** tab chats with any OpenAI-compatible API: OpenAI, Azure OpenAI, OpenRouter, vLLM or Ollama. Open **Settings → Assistant → Connection…** to choose:

- **Server connection.** The operator sets `--llm-url`, `--llm-model` and a key in `K8SFOAMS_LLM_API_KEY` or a file named by `--llm-api-key-file` (a mounted Secret). The key never reaches the browser and is only ever sent to `--llm-url`.
- **My own key.** URL, model and key typed in the browser. The key lasts until the tab closes unless you tick **Remember key in this browser**; then it stays in this browser's storage. The server relays it and never logs or stores it. On a shared deployment the URL's host must match `--llm-allowed-hosts`, and private addresses are refused.

```bash
k8sfoams --llm-url https://api.openai.com/v1 --llm-model gpt-4o-mini --llm-allowed-hosts api.openai.com
```

**What you'll see:** the Assistant tab, with **Settings → Assistant → Connection…** offering the server connection.

The column on the right lists what goes with your next message: a cluster summary, the Problems list, the pod and logs open in the Logs tab, and the last node you opened, which starts unticked. Untick anything you do not want to send. Nothing is sent until you press **Send**, **Analyze with assistant** in the Logs tab, or **Test and save** (which sends one short test message). The first time you open the tab it explains what leaves the cluster, until you press OK.

The assistant can also look things up itself, read-only: a cluster summary, the problem list, one pod or node, a container's logs (at most 500 lines or 32 KiB, newest kept), and the **Fit a pod** and **Drain a node** dry runs. Each lookup shows as a line in the answer. A question makes at most 16 lookups and 64 KiB of lookup output over 8 rounds, the last of which must answer without lookups; it stops early before a step that would pass the token cap. The lookup descriptions go out with every question and count toward the cap, so the composer's estimate includes them. Endpoints without tool support get a plain chat.

Every question has a token cap, 20,000 by default, set in Connection; on the server connection `--llm-max-tokens-per-question` (default 50000) is the ceiling. The composer shows an estimate of the next message against the cap, and Send is disabled when it is over. After each answer the tab shows the tokens actually used, as reported by the endpoint, or an estimate marked as such when it reports none.

{{< callout type="warning" title="Sharp edges" >}}
**Secrets.** The assistant reaches only what k8sfoams reads: nodes, pods and pod logs, never Secrets or ConfigMaps, and pod details without env values or annotations. Before anything is sent to the model endpoint, the server masks values that look like secrets: private keys, JWTs such as service account tokens, bearer and basic credentials, credentials in URLs, common API key formats, and the value after names like `password`, `secret`, `token` or `api_key`. Each answer says how many values were masked. Masking matches patterns, so an unusual secret can still pass; keep secrets out of logs. The Logs tab itself shows raw logs, since the viewer can already read them.
{{< /callout >}}
