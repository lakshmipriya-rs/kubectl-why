# kubectl-why

> Diagnose a single failing Kubernetes pod and print a root-cause report.

[![CI](https://github.com/lakshmipriya-rs/kubectl-why/actions/workflows/ci.yml/badge.svg)](https://github.com/lakshmipriya-rs/kubectl-why/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Why kubectl-why?

`kubectl describe pod` and `kubectl logs` give you raw data. `kubectl-why` gives you a **diagnosis**, where every finding points at the exact fields and events it is based on. It runs fully offline by default; add `--explain` for an optional LLM analysis that must cite that evidence:

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
# Diagnose a pod (rules only: no LLM, nothing leaves your machine)
kubectl why <pod> [-n namespace]

# Diagnose a workload: its unhealthiest pod is picked automatically
kubectl why deploy/web -n shop
kubectl why sts/postgres

# Diagnose a Service: selector, endpoints and ports, then its unhealthiest pod
kubectl why svc/web -n shop

# JSON output
kubectl why mypod --output json

# Also ask a local LLM (Ollama) for an analysis that cites the evidence
kubectl why mypod --explain
kubectl why mypod --explain --model mistral

# See exactly what --explain would send (redacted), without sending it
kubectl why mypod --dry-run
```

Supported targets: a pod name, or `<kind>/<name>` with kind `pod`, `deploy`, `sts`, `ds`, `rs`, `job` or `svc` (long names like `deployment` and `deployment.apps` work too). For a workload, pods are ranked by how clearly they are failing (Failed > crash-looping/pending > not ready > restarted), then by restart count, then newest first. The report says which pod was chosen and how many are unhealthy. A workload with **no pods** is reported as an error that points at quota or admission problems.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-n, --namespace` | `default` | Kubernetes namespace |
| `--explain` | `false` | Also ask an LLM for an analysis citing the rule evidence |
| `--dry-run` | `false` | Print the redacted payload `--explain` would send, and exit |
| `--provider` | `ollama` | LLM provider for `--explain` (currently `ollama`) |
| `--model` | `llama3` | Model for `--explain` |
| `-o, --output` | `text` | Output format: `text \| json` |
| `--kubeconfig` | _(standard lookup)_ | Override kubeconfig path |
| `--version` | | Print version, commit and build date |

`--no-llm` still works but is deprecated: rules-only is now the default.

---

## Architecture

```
kubectl why <pod> | <kind>/<name>
        │
        ▼
┌─────────────────┐
│   pkg/target    │  pod name, or a workload's unhealthiest pod
└────────┬────────┘
         │ pod name
         ▼
┌─────────────────┐
│   pkg/collect   │  reads pod spec/status, events, logs (last 100 lines),
│   Collector     │  owner chain, CM/Secret names, PVCs + their events, node conditions
└────────┬────────┘
         │ PodFacts
         ▼
┌─────────────────┐
│   pkg/rules     │  deterministic detectors: ImagePullBackOff, OOMKilled,
│   Rule Engine   │  SIGKILL, FailedScheduling, CrashLoopBackOff, ProbeFailure,
│                 │  MissingConfigMap, MissingSecret, UnboundPVC;
│                 │  for Services: SelectorMismatch, NoReadyEndpoints, ServicePortMismatch
└────────┬────────┘
         │ []Finding with evidence {id, source, field, value}
         ▼
┌─────────────────┐
│   pkg/redact    │  strips secrets, tokens, base64 blobs, credential patterns
│   Redactor      │  before any data leaves the machine
└────────┬────────┘
         │ RedactedFacts            ── only with --explain ──
         ▼
┌─────────────────┐
│   pkg/llm       │  Provider interface → Ollama
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
    resources: [pods/log, persistentvolumeclaims]
    verbs: [get]
  - apiGroups: [""]
    resources: [pods]
    verbs: [get, list]   # list: find a workload's pods
  - apiGroups: [""]
    resources: [events]
    verbs: [list]
  - apiGroups: [""]
    resources: [services]
    verbs: [get]         # kubectl why svc/<name>
  - apiGroups: [apps]
    resources: [deployments, replicasets, statefulsets, daemonsets]
    verbs: [get]
  - apiGroups: [batch]
    resources: [jobs]
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
| Container in `CrashLoopBackOff`: exit code explained (127 command not found, 0 exited but shouldn't, …) plus the last lines of the previous run's logs | `CrashLoopBackOff` |
| Failing liveness/readiness/startup probes: probe config shown next to the failure; wrong port, 404 path, timeouts and auth errors explained | `ProbeFailure` |
| Missing ConfigMap / Secret (or a key in one), from the kubelet's errors: points at the env, envFrom or volume that references it. Never reads ConfigMaps or Secrets itself. | `MissingConfigMap` / `MissingSecret` |
| PVC missing, Pending or Lost: reason taken from the PVC's own events (unknown StorageClass, no default class, provisioner failure). Ignores claims that are only waiting for the pod to be scheduled. | `UnboundPVC` |
| `kubectl why svc/<name>`: selector matches no pods; the closest pod and the differing label are named | `SelectorMismatch` |
| `kubectl why svc/<name>`: selected pods exist but none is Ready (then the pod is diagnosed too) | `NoReadyEndpoints` |
| `kubectl why svc/<name>`: `targetPort` is not a port the pods declare | `ServicePortMismatch` |

---

## LLM providers

The LLM is **optional** and only used with `--explain`. Without it, kubectl-why makes no network calls except to the Kubernetes API.

| Provider | Flag | Status |
|----------|------|--------|
| None (default) | _(no flag)_ | ✅ Rules-only, fully offline |
| Ollama (local) | `--explain --provider ollama` | ✅ Implemented |
| Hosted providers | | 🗺️ Planned. For Claude and other AI assistants, see the MCP server on the roadmap. |

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
