# kubectl-why

> Diagnose a single failing Kubernetes pod and print a root-cause report.

[![CI](https://github.com/assetworks/kubectl-why/actions/workflows/ci.yml/badge.svg)](https://github.com/assetworks/kubectl-why/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Why kubectl-why?

`kubectl describe pod` and `kubectl logs` give you raw data. `kubectl-why` gives you a **diagnosis**:

```
🔴 [ImagePullBackOff] One or more containers cannot pull their image.
   Evidence:
     • container "app": waiting reason="ImagePullBackOff" message="Back-off pulling image"
     • image: "nginx:doesnotexist999"
     • event "Failed": Back-off pulling image "nginx:doesnotexist999"

🤖 LLM Analysis  (confidence: high)
────────────────────────────────────────────────────────────
Root cause:  The image tag "doesnotexist999" does not exist on Docker Hub.

Causal chain:
  1. Deployment specifies image nginx:doesnotexist999
  2. kubelet cannot pull the image (tag not found)
  3. Container enters ImagePullBackOff with exponential back-off

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
go install github.com/assetworks/kubectl-why@latest
```

### Pre-built binary

Download from [Releases](https://github.com/assetworks/kubectl-why/releases).

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
         │ Response {root_cause, causal_chain, evidence[], fix, confidence}
         ▼
┌─────────────────┐
│  pkg/validate   │  drops LLM claims with no matching evidence item
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   pkg/report    │  text (colored terminal) or JSON output
└─────────────────┘
```

---

## RBAC

`kubectl-why` is **read-only**. Minimum RBAC:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubectl-why-reader
rules:
  - apiGroups: [""]
    resources:
      - pods
      - pods/log
      - events
      - configmaps
      - secrets
      - persistentvolumeclaims
      - nodes
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources:
      - replicasets
      - deployments
    verbs: [get, list, watch]
```

> **Note:** Secret *contents* are never read. Only names are collected to report which secrets a pod references.

---

## Supported failure modes

| Rule | Category |
|------|----------|
| Container waiting with `ImagePullBackOff` or `ErrImagePull` | `ImagePullBackOff` |
| Exit code 137 or `OOMKilled` reason | `OOMKilled` |
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

See [`eval/README.md`](eval/README.md) for how to run the 25+ broken-manifest evaluation suite against a kind cluster.

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

[MIT](LICENSE) © AssetWorks Fleet
