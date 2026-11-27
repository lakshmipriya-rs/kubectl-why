# Eval Suite

The eval suite applies deliberately broken manifests to a [kind](https://kind.sigs.k8s.io/) cluster, runs `kubectl-why` against the failing pod, and scores the JSON output.

Each scenario runs in its own namespace (`why-eval-<scenario>`), which is deleted afterwards, so scenarios never pick up each other's pods or anything in `default`.

## Scoring

| Metric | Points |
|--------|--------|
| Expected category present in `findings` | 1 |
| Each fix keyword present in the output | 0.5 |
| Each must-not-hallucinate term present | −1 |

A scenario **passes** when it ran without errors, the expected category was reported, and no must-not-hallucinate term appeared. The harness exits non-zero if any scenario fails, so it can gate CI.

## Running

```bash
# 1. Start a kind cluster
kind create cluster --name why-eval

# 2. Build kubectl-why
go build -o kubectl-why .

# 3. Run all scenarios (rules-only, no LLM)
go run ./eval/harness --scenarios ./eval/scenarios --binary ./kubectl-why

# Run one scenario, or also test the LLM analysis (--explain)
go run ./eval/harness --binary ./kubectl-why --run 04
go run ./eval/harness --binary ./kubectl-why --llm

# 4. Clean up
kind delete cluster --name why-eval
```

### Behind a TLS-intercepting proxy

If pods sit in `ImagePullBackOff` with `x509: certificate signed by unknown authority`, kind's containerd doesn't trust your network's proxy certificate. Pull the images on the host and load them into kind instead (the manifests use `imagePullPolicy: IfNotPresent`):

```bash
docker pull --platform linux/amd64 busybox:1.36
docker save --platform linux/amd64 busybox:1.36 -o busybox.tar
kind load image-archive busybox.tar --name why-eval
```

(`kind load docker-image` can fail on multi-architecture images with `content digest … not found`; saving a single platform avoids that. A `mismatched image rootfs` warning from `kind load image-archive` is harmless.)

All scenarios use only the official `busybox:1.36` image, apart from scenario 01, whose image tag is meant not to exist.

| Flag | Default | Description |
|------|---------|-------------|
| `--scenarios` | `eval/scenarios` | Scenarios directory |
| `--binary` | `kubectl-why` | kubectl-why binary to test |
| `--run` | _(all)_ | Only scenarios whose directory name contains this |
| `--llm` | `false` | Run kubectl-why with `--explain` (needs a local Ollama) |
| `--timeout` | `3m` | Per-scenario wait for the failure state |

## Scenarios

| # | Name | Expected category |
|---|------|------------------|
| 01 | bad-image | `ImagePullBackOff` |
| 02 | oomkilled | `OOMKilled` |
| 03 | unschedulable | `FailedScheduling` |
| 04 | liveness-sigkill | `SIGKILL` |
| 05 | crashloop (as `deploy/crashy`) | `CrashLoopBackOff` |
| 06 | readiness-404 (as `deploy/web`) | `ProbeFailure` |
| 07 | missing-configmap (as `deploy/config-app`) | `MissingConfigMap` |
| 08 | missing-secret-volume | `MissingSecret` |
| 09 | unbound-pvc | `UnboundPVC` |
| 10 | service-selector (as `svc/web`) | `SelectorMismatch` |
| 11 | quota-failedcreate (as `deploy/api`) | `ReplicaFailure` |

## Adding a scenario

Create `eval/scenarios/NN-name/` with:

- `manifest.yaml`: the broken workload. **Do not set `metadata.namespace`**; the harness applies it into the scenario's namespace.
- `ground-truth.json`:

```json
{
  "scenario": "NN-name",
  "description": "What is broken",
  "expected_category": "OOMKilled",
  "fix_keywords": ["memory", "limit"],
  "must_not_hallucinate": ["ImagePullBackOff"],
  "pod_selector": "app=web",
  "target": "deploy/web",
  "wait_for": {
    "pod_phase": "Pending",
    "container_waiting_reason": "ImagePullBackOff",
    "container_terminated_reason": "OOMKilled",
    "exit_code": 137,
    "event_reason": "FailedScheduling",
    "min_restarts": 2
  }
}
```

All `wait_for` fields are optional, but set at least one. Every field that is set must match. Container conditions match on any container or init container, including its previous (restarted) state. For crash loops, prefer `min_restarts` plus `exit_code` over `container_waiting_reason: CrashLoopBackOff`: newer Kubernetes versions (e.g. 1.35) report a crash-looping container as `terminated` for most of the back-off, so the waiting reason is only visible briefly. `pod_selector` is only needed when a scenario creates several pods. `target` is passed to kubectl-why instead of the failing pod's name, to test workload targets such as `deploy/web`.

`go test ./eval/...` checks that every scenario's files are valid.
