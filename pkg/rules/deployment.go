// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

const revisionAnnotation = "deployment.kubernetes.io/revision"

// DeploymentRuleFunc is the signature of rules that diagnose a Deployment.
type DeploymentRuleFunc func(df *collect.DeploymentFacts) *Finding

var deploymentRegistry = []DeploymentRuleFunc{
	CheckReplicaFailure,
	CheckRolloutStuck,
}

// RunDeploymentRules runs every Deployment rule. Callers merging these with
// pod findings should call AssignEvidenceIDs again on the combined list.
func RunDeploymentRules(df *collect.DeploymentFacts) []Finding {
	var findings []Finding
	for _, rule := range deploymentRegistry {
		if f := rule(df); f != nil {
			findings = append(findings, *f)
		}
	}
	AssignEvidenceIDs(findings)
	return findings
}

// CheckReplicaFailure reports a Deployment whose ReplicaSet cannot create
// pods: rejected by a ResourceQuota, Pod Security admission, an OpenShift
// SCC, a LimitRange or an admission webhook.
func CheckReplicaFailure(df *collect.DeploymentFacts) *Finding {
	if df == nil || df.Deployment == nil {
		return nil
	}
	d := df.Deployment
	src := "Deployment/" + d.Name

	var (
		evidence []Evidence
		messages []string
	)
	if c := deploymentCondition(d, appsv1.DeploymentReplicaFailure); c != nil && c.Status == corev1.ConditionTrue {
		evidence = append(evidence, Evidence{
			Source: src, Field: "status.conditions[ReplicaFailure]",
			Value: fmt.Sprintf("True, %s: %s", c.Reason, c.Message),
		})
		messages = append(messages, c.Message)
	}
	for _, rs := range sortedByRevision(df.ReplicaSets) {
		for _, evt := range df.ReplicaSetEvents[rs.Name] {
			if evt.Reason != "FailedCreate" {
				continue
			}
			value := evt.Message
			if evt.Count > 1 {
				value = fmt.Sprintf("%s (x%d)", evt.Message, evt.Count)
			}
			evidence = append(evidence, Evidence{Source: "ReplicaSet/" + rs.Name, Field: "event FailedCreate", Value: value})
			messages = append(messages, evt.Message)
		}
	}
	if len(messages) == 0 {
		return nil
	}

	desired := desiredReplicas(d)
	evidence = append(evidence, Evidence{
		Source: src, Field: "status",
		Value: fmt.Sprintf("%d desired, %d created, %d ready", desired, d.Status.Replicas, d.Status.ReadyReplicas),
	})

	detail := fmt.Sprintf("Deployment %q can't create all of its pods (%d of %d exist). %s",
		d.Name, d.Status.Replicas, desired, failedCreateReason(messages[len(messages)-1], d.Namespace))
	return &Finding{Category: CategoryReplicaFailure, Detail: detail, Evidence: evidence, Severity: "critical"}
}

// failedCreateReason explains a pod-creation failure from its message.
func failedCreateReason(msg, namespace string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "exceeded quota"):
		return fmt.Sprintf("Pods are rejected by a ResourceQuota in namespace %q. Raise the quota, lower the pods' resource requests, or reduce replicas.", namespace)
	case strings.Contains(lower, "security context constraint"):
		return "Pods are rejected by OpenShift Security Context Constraints: they ask for something (e.g. running as root, a fixed user ID, host access) that no SCC available to the pod's service account allows. Make the image run as an arbitrary non-root user, or grant a suitable SCC."
	case strings.Contains(lower, "violates podsecurity"):
		return "Pods are rejected by Pod Security admission (the namespace's pod-security level). Adjust the pod's securityContext to meet the level, or change the namespace's level."
	case strings.Contains(lower, "usage per container") || strings.Contains(lower, "usage per pod") ||
		strings.Contains(lower, "must be less than or equal to") || strings.Contains(lower, "limitrange"):
		return "Pods violate a LimitRange in the namespace (resource requests or limits out of the allowed range). Adjust the container resources."
	case strings.Contains(lower, "admission webhook"):
		return "An admission webhook rejected the pods. Its message (in the evidence) says why."
	case strings.Contains(lower, "serviceaccount") && strings.Contains(lower, "not found"):
		return "The pods' ServiceAccount does not exist. Create it or fix serviceAccountName."
	default:
		return "The ReplicaSet's pod creation is rejected; the message in the evidence says why."
	}
}

// CheckRolloutStuck reports a Deployment rollout that exceeded its progress deadline.
func CheckRolloutStuck(df *collect.DeploymentFacts) *Finding {
	if df == nil || df.Deployment == nil {
		return nil
	}
	d := df.Deployment
	c := deploymentCondition(d, appsv1.DeploymentProgressing)
	if c == nil || c.Status != corev1.ConditionFalse || c.Reason != "ProgressDeadlineExceeded" {
		return nil
	}
	src := "Deployment/" + d.Name
	desired := desiredReplicas(d)

	evidence := []Evidence{
		{Source: src, Field: "status.conditions[Progressing]", Value: fmt.Sprintf("False, %s: %s", c.Reason, c.Message)},
		{Source: src, Field: "status", Value: fmt.Sprintf("%d desired, %d updated, %d ready, %d available",
			desired, d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Status.AvailableReplicas)},
	}

	newRev := d.Annotations[revisionAnnotation]
	oldReady := 0
	for _, rs := range sortedByRevision(df.ReplicaSets) {
		total, ready := podsOf(rs, df.Pods)
		if total == 0 {
			continue
		}
		role := "old"
		if rs.Annotations[revisionAnnotation] == newRev {
			role = "new"
		} else {
			oldReady += ready
		}
		evidence = append(evidence, Evidence{
			Source: "ReplicaSet/" + rs.Name,
			Field:  "pods",
			Value:  fmt.Sprintf("%d, %d ready (revision %s, %s)", total, ready, rs.Annotations[revisionAnnotation], role),
		})
	}

	deadline := int32(600)
	if d.Spec.ProgressDeadlineSeconds != nil {
		deadline = *d.Spec.ProgressDeadlineSeconds
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "The rollout of Deployment %q to revision %s is stuck: it made no progress within progressDeadlineSeconds (%ds). ",
		d.Name, newRev, deadline)
	fmt.Fprintf(&sb, "%d of %d replicas run the new version. ", d.Status.UpdatedReplicas, desired)
	if oldReady > 0 {
		fmt.Fprintf(&sb, "The previous version still serves %d ready pod(s), so traffic may still work on the old version. ", oldReady)
	}
	sb.WriteString("The pod findings explain why the new pods fail. ")
	fmt.Fprintf(&sb, "To roll back: kubectl rollout undo deployment/%s -n %s", d.Name, d.Namespace)

	severity := "warning"
	if d.Status.AvailableReplicas == 0 {
		severity = "critical"
	}
	return &Finding{Category: CategoryRolloutStuck, Detail: sb.String(), Evidence: evidence, Severity: severity}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func deploymentCondition(d *appsv1.Deployment, t appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range d.Status.Conditions {
		if d.Status.Conditions[i].Type == t {
			return &d.Status.Conditions[i]
		}
	}
	return nil
}

func desiredReplicas(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 1 // Kubernetes default
	}
	return *d.Spec.Replicas
}

// sortedByRevision returns ReplicaSets newest revision first.
func sortedByRevision(rss []appsv1.ReplicaSet) []appsv1.ReplicaSet {
	out := append([]appsv1.ReplicaSet(nil), rss...)
	rev := func(rs appsv1.ReplicaSet) int {
		n, _ := strconv.Atoi(rs.Annotations[revisionAnnotation])
		return n
	}
	sort.SliceStable(out, func(i, j int) bool { return rev(out[i]) > rev(out[j]) })
	return out
}

// podsOf counts the active pods controlled by rs, and how many are Ready.
func podsOf(rs appsv1.ReplicaSet, pods []corev1.Pod) (total, ready int) {
	for _, p := range activePods(pods) {
		for _, ref := range p.OwnerReferences {
			if ref.Controller != nil && *ref.Controller && ref.UID == rs.UID {
				total++
				if podReady(p) {
					ready++
				}
			}
		}
	}
	return total, ready
}
