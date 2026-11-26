// SPDX-License-Identifier: MIT

package rules

import "fmt"

// Category names for deterministic findings.
const (
	CategoryImagePullBackOff = "ImagePullBackOff"
	CategoryOOMKilled        = "OOMKilled"
	CategorySIGKILL          = "SIGKILL"
	CategoryFailedScheduling = "FailedScheduling"
	CategoryCrashLoopBackOff = "CrashLoopBackOff"
	CategoryProbeFailure     = "ProbeFailure"
	CategoryMissingConfigMap = "MissingConfigMap"
	CategoryMissingSecret    = "MissingSecret"
	CategoryUnboundPVC       = "UnboundPVC"
	CategorySelectorMismatch = "SelectorMismatch"
)

// Finding is a structured diagnostic result from the rule engine.
type Finding struct {
	// Category is a machine-readable failure category (see constants above).
	Category string `json:"category"`

	// Detail is a human-readable explanation of what was detected.
	Detail string `json:"detail"`

	// Evidence lists the observed facts that support this finding.
	Evidence []Evidence `json:"evidence"`

	// Severity is "critical", "warning", or "info".
	Severity string `json:"severity"`
}

// Evidence is one observed fact, traceable to the Kubernetes object and field
// it was read from. Every claim in a report must point at Evidence.
type Evidence struct {
	// ID is unique within one report ("E1", "E2", …). It is assigned by RunAll
	// so that an LLM (or a human) can cite exactly which facts support a claim.
	ID string `json:"id"`

	// Source is the object the fact came from: "Pod/web-1", "Node/worker-1",
	// or "Event" for an event about the pod.
	Source string `json:"source"`

	// Field locates the fact inside Source, using kubectl-style paths such as
	// "status.containerStatuses[app].state.waiting.reason". For events it is
	// the event reason, e.g. "Failed".
	Field string `json:"field"`

	// Value is what was observed.
	Value string `json:"value"`
}

// String renders evidence on one line, e.g.
// "Pod/web status.containerStatuses[app].state.waiting.reason: ImagePullBackOff".
func (e Evidence) String() string {
	return fmt.Sprintf("%s %s: %s", e.Source, e.Field, e.Value)
}
