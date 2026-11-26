# kubectl-why

> Diagnose a single failing Kubernetes pod and print a root-cause report.

[![CI](https://github.com/lakshmipriya-rs/kubectl-why/actions/workflows/ci.yml/badge.svg)](https://github.com/lakshmipriya-rs/kubectl-why/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Why kubectl-why?

`kubectl describe pod` and `kubectl logs` give you raw data. `kubectl-why` gives you a **diagnosis**:

```
🔍 Rule Engine Findings
────────────────────────────────────────────────────────────
🔴 [ImagePullBackOff] One or more containers cannot pull their image. Check that the image tag exists and that imagePullSecrets are configured correctly.
   Evidence:
     • [E1] Pod/bad-image-7d9f status.containerStatuses[app].state.waiting.reason: ImagePullBackOff
     • [E2] Pod/bad-image-7d9f status.containerStatuses[app].image: nginx:doesnotexist999
     • [E3] Pod/bad-image-7d9f status.containerStatuses[app].state.waiting.message: Back-off pulling image "nginx:doesnotexist999"
     • [E4] Event Failed: Failed to pull image "nginx:doesnotexist999": not found (x3)

🤖 LLM Analysis  (confidence: high)
────────────────────────────────────────────────────────────
Root cause:  The image tag "doesnotexist999" does not exist on Docker Hub.

Causal chain:
  1. The pod spec uses image nginx:doesnotexist999
  2. The registry reports the tag as not found
  3. The kubelet backs off and retries (ImagePullBackOff)

Cited evidence:
  • [E2] Pod/bad-image-7d9f status.containerStatuses[app].image: nginx:doesnotexist999
  • [E4] Event Failed: Failed to pull image "nginx:doesnotexist999": not found (x3)

Suggested fix:
  Update the image tag to a valid tag (e.g. nginx:stable or nginx:1.27).
```

---

## Installation

### Krew (recommended)

```bash
kubectl krew install why
```

### Go install

```bash
go install github.com/lakshmipriya-rs/kubectl-why@latest
```

### Pre-built binary

Download from [Releases](https://github.com/lakshmipriya-rs/kubectl-why/releases).

---

## Usage

```bash
# Diagnose a pod (uses Ollama by default)
kubectl why <pod> [-n namespace]

# Rules-only, no LLM call
kubectl why mypod --no-llm

# Print redacted payload without calling the LLM
kubectl why mypod --dry-run

# JSON output
kubectl why mypod --output json

# Use a specific provider / model
kubectl why mypod --provider ollama --model mistral
kubectl why mypod --provider anthropic --model claude-3-5-haiku-20241022
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-n, --namespace` | `default` | Kubernetes namespace |
| `--no-llm` | `false` | Rules-only; no network calls |
| `--dry-run` | `false` | Print redacted payload, skip LLM |
| `--provider` | `ollama` | LLM provider: `ollama \| anthropic \| openai` |
| `--model` | _(provider default)_ | Model name override |
| `--output` | `text` | Output format: `text \| json` |
| `--kubeconfig` | _(standard lookup)_ | Override kubeconfig path |

---

## Architecture

```
kubectl why <pod>
        │
        ▼
┌─────────────────┐
│   pkg/collect   │  reads pod spec/status, events, logs (last 100 lines),
│   Collector     │  owner chain, CM/Secret names, PVCs, node conditions
└────────┬────────┘
         │ PodFacts
         ▼
┌─────────────────┐
│   pkg/rules     │  deterministic detectors: ImagePullBackOff, OOMKilled,
│   Rule Engine   │  FailedScheduling, CrashLoopBackOff, probe failures,
└────────┬────────┘  missing CM/Secret, unbound PVC, selector mismatch
         │ []Finding
         ▼
┌─────────────────┐
│   pkg/redact    │  strips secrets, tokens, base64 blobs, credential patterns
│   Redactor      │  before any data leaves the machine
└────────┬────────┘
         │ RedactedFacts
         ▼
┌─────────────────┐
│   pkg/llm       │  Provider interface → Ollama (default) / Anthropic / OpenAI
│   LLM Layer     │  Logs/events wrapped in delimiter blocks (prompt injection guard)
└────────┬────────┘
         │ Response {root_cause, causal_chain, evidence_ids[], fix, confidence}
         ▼
┌─────────────────┐
│  pkg/validate   │  keeps only evidence IDs that exist; uncited claims → low confidence
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   pkg/report    │  text (colored terminal) or JSON output
└─────────────────┘
```

---

## Evidence model

Every finding lists the facts it is based on. Each evidence item records where it came from, so you can check it yourself with `kubectl get -o yaml`:

```json
{
  "category": "OOMKilled",
  "detail": "One or more containers were killed by the Linux OOM killer. …",
  "severity": "critical",
  "evidence": [
    {"id": "E1", "source": "Pod/oomkilled-pod", "field": "status.containerStatuses[stress].state.terminated.reason", "value": "OOMKilled"},
    {"id": "E2", "source": "Pod/oomkilled-pod", "field": "status.containerStatuses[stress].state.terminated.exitCode", "value": "137"},
    {"id": "E3", "source": "Pod/oomkilled-pod", "field": "spec.containers[stress].resources.limits.memory", "value": "10Mi"}
  ]
}
```

- `source` is the object (`Pod/<name>`, `Node/<name>`) or `Event` for an event about the pod.
- `field` is a kubectl-style path into that object (for events, the event reason).
- `id` is unique within one report. When an LLM is used, it must cite these IDs in `evidence_ids`; unknown IDs are dropped, and a root cause with no valid citation is shown as uncited with confidence `low`.

---

## RBAC

`kubectl-why` is **read-only** and only uses `get` and `list`. Minimum RBAC:

```yaml
# Namespaced: grant per namespace you want to diagnose (RoleBinding).
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubectl-why-reader
rules:
  - apiGroups: [""]
    resources: [pods, pods/log, persistentvolumeclaims]
    verbs: [get]
  - apiGroups: [""]
    resources: [events]
    verbs: [list]
  - apiGroups: [apps]
    resources: [replicasets, deployments]
    verbs: [get]
---
# Optional, cluster-scoped: lets kubectl-why read the conditions of the node a
# pod runs on (MemoryPressure, DiskPressure…). Without it, that evidence is skipped.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubectl-why-node-reader
rules:
  - apiGroups: [""]
    resources: [nodes]
    verbs: [get]
```

> **Note:** No access to Secrets or ConfigMaps is needed. Their *names* are read from the pod spec; their contents are never fetched.
>
> **Redaction:** logs, event messages and rule evidence are scrubbed of credential-like patterns before anything is sent to an LLM or printed by `--dry-run`. The local report shows rule evidence unredacted, because it never leaves your terminal.

---

## Supported failure modes

| Rule | Category |
|------|----------|
| Container waiting with `ImagePullBackOff` or `ErrImagePull` | `ImagePullBackOff` |
| Container terminated with `OOMKilled` reason | `OOMKilled` |
| Exit code 137 (SIGKILL) without `OOMKilled` — probe kill, grace period, eviction | `SIGKILL` |
| `FailedScheduling` events while Pending | `FailedScheduling` |
| _(planned)_ CrashLoopBackOff | `CrashLoopBackOff` |
| _(planned)_ Failing liveness/readiness probes | `ProbeFailure` |
| _(planned)_ Missing ConfigMap / Secret | `MissingConfigMap` / `MissingSecret` |
| _(planned)_ Unbound PVC | `UnboundPVC` |
| _(planned)_ Service selector mismatch | `SelectorMismatch` |

---

## LLM providers

| Provider | Flag | Status |
|----------|------|--------|
| Ollama (local) | `--provider ollama` | ✅ Implemented |
| Anthropic | `--provider anthropic` | 🚧 Stub (coming soon) |
| OpenAI | `--provider openai` | 🚧 Stub (coming soon) |
| None | `--no-llm` | ✅ Rules-only mode |

---

## Eval suite

See [`eval/README.md`](eval/README.md) for how to run the broken-manifest evaluation suite against a kind cluster. Every rule should ship with a scenario.

---

## Roadmap

- `--suggest-patch`: generate a corrective patch, validate with `kubectl apply --dry-run=server`
- "what changed": diff between last healthy and current revision
- MCP server mode: expose collector and rules as tools for AI agents
- GitHub Action for diagnosing CI failures

---

## Contributing

PRs welcome. Please run `go test ./...` and `go vet ./...` before opening a PR.

## License

[MIT](LICENSE) © Lakshmipriya
