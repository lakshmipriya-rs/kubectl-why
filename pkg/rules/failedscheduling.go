// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// CheckFailedScheduling detects pods stuck in Pending due to scheduling failures.
func CheckFailedScheduling(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}
	if facts.Pod.Status.Phase != corev1.PodPending {
		return nil
	}

	var evidence []Evidence
	for _, evt := range facts.Events {
		if evt.Reason == "FailedScheduling" {
			evidence = append(evidence, eventEvidence(evt))
		}
	}

	// Surface node conditions too (e.g. DiskPressure, MemoryPressure).
	for _, cond := range facts.NodeConditions {
		if cond.Status == corev1.ConditionTrue &&
			(cond.Type == corev1.NodeDiskPressure ||
				cond.Type == corev1.NodeMemoryPressure ||
				cond.Type == corev1.NodePIDPressure ||
				cond.Type == corev1.NodeNetworkUnavailable) {
			evidence = append(evidence, Evidence{
				Source: "Node/" + facts.Pod.Spec.NodeName,
				Field:  fmt.Sprintf("status.conditions[%s]", cond.Type),
				Value:  fmt.Sprintf("True: %s", cond.Message),
			})
		}
	}

	if len(evidence) == 0 {
		return nil
	}

	// "Insufficient cpu/memory": show what the pod asks for.
	joined := strings.ToLower(evidenceValues(evidence))
	for _, res := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		if !strings.Contains(joined, "insufficient "+string(res)) {
			continue
		}
		for _, c := range facts.Pod.Spec.Containers {
			if q, ok := c.Resources.Requests[res]; ok {
				evidence = append(evidence, Evidence{
					Source: podSource(facts.Pod),
					Field:  fmt.Sprintf("%s.resources.requests.%s", specField(false, c.Name), res),
					Value:  formatQuantity(res, q),
				})
			}
		}
	}

	detail := buildSchedulingDetail(evidence)

	return &Finding{
		Category: CategoryFailedScheduling,
		Detail:   detail,
		Evidence: evidence,
		Severity: "critical",
	}
}

// buildSchedulingDetail parses common scheduling error phrases to produce
// a more actionable detail string.
func buildSchedulingDetail(evidence []Evidence) string {
	lower := strings.ToLower(evidenceValues(evidence))

	switch {
	case strings.Contains(lower, "insufficient cpu"):
		return "Pod cannot be scheduled: insufficient CPU on available nodes. Reduce CPU requests or add more nodes."
	case strings.Contains(lower, "insufficient memory"):
		return "Pod cannot be scheduled: insufficient memory on available nodes. Reduce memory requests or add more nodes."
	case strings.Contains(lower, "node(s) had taint"):
		return "Pod cannot be scheduled: nodes have taints that the pod does not tolerate. Add tolerations to the pod spec."
	case strings.Contains(lower, "node selector") || strings.Contains(lower, "didn't match node selector"):
		return "Pod cannot be scheduled: no nodes match the pod's nodeSelector. Check nodeSelector labels."
	case strings.Contains(lower, "unbound immediate persistentvolumeclaims"):
		return "Pod cannot be scheduled: a PersistentVolumeClaim is unbound. Check PVC status and available storage."
	default:
		return "Pod is stuck in Pending due to scheduling failure. See Evidence for details."
	}
}

func evidenceValues(evidence []Evidence) string {
	var sb strings.Builder
	for _, e := range evidence {
		sb.WriteString(e.Value)
		sb.WriteString(" ")
	}
	return sb.String()
}

// formatQuantity shows whole CPU cores as plain numbers ("1000", not "1k")
// and everything else in Kubernetes notation ("500m", "64Mi").
func formatQuantity(res corev1.ResourceName, q resource.Quantity) string {
	if res == corev1.ResourceCPU && q.MilliValue()%1000 == 0 {
		return fmt.Sprintf("%d", q.MilliValue()/1000)
	}
	return q.String()
}
