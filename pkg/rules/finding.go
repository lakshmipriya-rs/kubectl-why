// SPDX-License-Identifier: MIT

package rules

// Category names for deterministic findings.
const (
	CategoryImagePullBackOff  = "ImagePullBackOff"
	CategoryOOMKilled         = "OOMKilled"
	CategoryFailedScheduling  = "FailedScheduling"
	CategoryCrashLoopBackOff  = "CrashLoopBackOff"
	CategoryProbeFailure      = "ProbeFailure"
	CategoryMissingConfigMap  = "MissingConfigMap"
	CategoryMissingSecret     = "MissingSecret"
	CategoryUnboundPVC        = "UnboundPVC"
	CategorySelectorMismatch  = "SelectorMismatch"
)

// Finding is a structured diagnostic result from the rule engine.
type Finding struct {
	// Category is a machine-readable failure category (see constants above).
	Category string `json:"category"`

	// Detail is a human-readable explanation of what was detected.
	Detail string `json:"detail"`

	// Evidence is a list of raw strings (event messages, container names,
	// log snippets) that directly support this finding.
	Evidence []string `json:"evidence"`

	// Severity is "critical", "warning", or "info".
	Severity string `json:"severity"`
}
