# Changelog

All notable changes to kubectl-why are documented here.
Versions follow [Semantic Versioning](https://semver.org/); while the version is 0.x, minor releases may contain breaking changes.

## [Unreleased]

### Added
- `kubectl-why mcp`: a Model Context Protocol server for Claude Desktop, Claude Code and other MCP clients, with read-only tools `diagnose`, `list_unhealthy` and `get_events`. Results are redacted and marked as untrusted cluster data; `--allowed-namespaces` limits what can be read.

### Changed
- Requires Go 1.25 to build (the MCP SDK needs it; Go 1.24 is out of support).
- `-n/--namespace` and `--kubeconfig` are accepted by subcommands too.
- Without `-n`, the namespace now comes from the current kubeconfig context (as with kubectl and `oc project`) instead of always `default`.

### Internal
- The diagnosis pipeline moved from the CLI into `pkg/diagnose`, so the CLI and the upcoming MCP server share it.

## [0.3.0] - 2026-11-27

First tagged release. kubectl-why diagnoses failing pods, workloads and Services and prints a root-cause report in which every finding cites the exact fields and events it is based on. It runs fully offline by default.

### Diagnoses

| Target | Findings |
|---|---|
| Pod | `ImagePullBackOff`, `CrashLoopBackOff` (exit code explained, last log lines of the previous run), `OOMKilled`, `SIGKILL` (exit 137 without OOM), `ProbeFailure` (probe config next to the failure, wrong port/path/timeouts explained), `MissingConfigMap`, `MissingSecret` (from kubelet errors; no Secret access needed), `UnboundPVC` (reason from the PVC's own events), `FailedScheduling` (with the pod's requests) |
| `svc/<name>` | `SelectorMismatch` (closest pod and differing label named), `NoReadyEndpoints` (plus the pod's diagnosis), `ServicePortMismatch` |
| `deploy/<name>` | `ReplicaFailure` (ResourceQuota, Pod Security, OpenShift SCC, LimitRange, webhook, missing ServiceAccount), `RolloutStuck` (new vs old ReplicaSet, rollback command) |

### Added
- Workload and Service targets: `kubectl why deploy/web`, `sts/…`, `ds/…`, `rs/…`, `job/…`, `svc/…`. For a workload, its unhealthiest pod is diagnosed and the report says which one was chosen.
- Structured evidence: each finding lists `{id, source, field, value}` items (e.g. `[E1] Pod/web status.containerStatuses[app].state.waiting.reason: ImagePullBackOff`).
- `--explain`: optional LLM analysis (local Ollama) that must cite evidence IDs; unknown IDs are dropped and uncited claims are marked low confidence.
- `--dry-run`: print the redacted payload `--explain` would send.
- `--version`, and `-o` as shorthand for `--output`.
- Eval suite: 11 broken-manifest scenarios run on a kind cluster in CI.

### Changed (breaking)
- **Rules-only is the default.** No LLM is called and no data leaves the machine unless `--explain` is passed. `--no-llm` still works but is deprecated.
- **JSON output:** `findings[].evidence` is now a list of objects (`id`, `source`, `field`, `value`) instead of strings, `llm.evidence` is replaced by `llm.evidence_ids`, and a `target` object is added.
- `--provider` accepts only `ollama`; the unimplemented `anthropic` and `openai` stubs were removed.

### Fixed
- ImagePullBackOff was reported for crash-looping pods (any `BackOff` event triggered it).
- Exit code 137 was always reported as OOMKilled; it is now `SIGKILL` unless the kernel reports an OOM kill.
- Node conditions were almost never collected.
- Rule evidence is now redacted before it is sent to an LLM or printed by `--dry-run`.
- The Ollama call had no timeout; Ctrl-C now cancels cleanly.

### Security
- Least-privilege RBAC: no access to Secrets or ConfigMaps is needed. See the README for the minimal Role.

[0.3.0]: https://github.com/lakshmipriya-rs/kubectl-why/releases/tag/v0.3.0
