// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

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

	var evidence []string

	for _, evt := range facts.Events {
		if evt.Reason == "FailedScheduling" {
			evidence = append(evidence, fmt.Sprintf("event FailedScheduling: %s", evt.Message))
		}
	}

	// Surface node conditions too (e.g. DiskPressure, MemoryPressure).
	for _, cond := range facts.NodeConditions {
		if cond.Status == corev1.ConditionTrue &&
			(cond.Type == corev1.NodeDiskPressure ||
				cond.Type == corev1.NodeMemoryPressure ||
				cond.Type == corev1.NodePIDPressure ||
				cond.Type == corev1.NodeNetworkUnavailable) {
			evidence = append(evidence, fmt.Sprintf("node condition %s=True: %s", cond.Type, cond.Message))
		}
	}

	if len(evidence) == 0 {
		return nil
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
func buildSchedulingDetail(evidence []string) string {
	joined := strings.Join(evidence, " ")
	lower := strings.ToLower(joined)

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
