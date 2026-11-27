// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// RuleFunc is the signature every rule must implement.
// It returns nil when the rule does not apply.
type RuleFunc func(facts *collect.PodFacts) *Finding

// registry holds all registered rules in priority order.
var registry = []RuleFunc{
	CheckImagePullBackOff,
	CheckOOMKilled,
	CheckSIGKILL,
	CheckFailedScheduling,
}

// RunAll executes every registered rule against facts and returns all findings.
// Rules are independent; multiple findings may be returned (e.g. two problems at once).
// Evidence IDs (E1, E2, …) are assigned in order across all findings.
func RunAll(facts *collect.PodFacts) []Finding {
	var findings []Finding
	for _, rule := range registry {
		if f := rule(facts); f != nil {
			findings = append(findings, *f)
		}
	}
	AssignEvidenceIDs(findings)
	return findings
}

// AssignEvidenceIDs numbers every evidence item across findings as E1, E2, …
func AssignEvidenceIDs(findings []Finding) {
	n := 0
	for i := range findings {
		for j := range findings[i].Evidence {
			n++
			findings[i].Evidence[j].ID = fmt.Sprintf("E%d", n)
		}
	}
}

// ── Evidence constructors shared by rules ────────────────────────────────────

func podSource(pod *corev1.Pod) string {
	return "Pod/" + pod.Name
}

// eventEvidence records an event about the pod. Repeated events show their count.
func eventEvidence(evt corev1.Event) Evidence {
	value := evt.Message
	if evt.Count > 1 {
		value = fmt.Sprintf("%s (x%d)", evt.Message, evt.Count)
	}
	return Evidence{Source: "Event", Field: evt.Reason, Value: value}
}

// statusField returns the kubectl-style path prefix for a container status:
// "status.containerStatuses[app]" or "status.initContainerStatuses[init]".
func statusField(init bool, container string) string {
	if init {
		return fmt.Sprintf("status.initContainerStatuses[%s]", container)
	}
	return fmt.Sprintf("status.containerStatuses[%s]", container)
}

// specField returns "spec.containers[app]" or "spec.initContainers[init]".
func specField(init bool, container string) string {
	if init {
		return fmt.Sprintf("spec.initContainers[%s]", container)
	}
	return fmt.Sprintf("spec.containers[%s]", container)
}
