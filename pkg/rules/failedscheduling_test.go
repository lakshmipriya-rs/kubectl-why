// SPDX-License-Identifier: MIT

package rules_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func pendingPodWithEvent(msg string) *collect.PodFacts {
	return &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "sched-pod"},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
		Events: []corev1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "e1"},
				Reason:     "FailedScheduling",
				Message:    msg,
			},
		},
	}
}

func TestFailedScheduling_InsufficientCPU(t *testing.T) {
	f := rules.CheckFailedScheduling(pendingPodWithEvent("0/3 nodes are available: insufficient cpu"))
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryFailedScheduling {
		t.Errorf("category = %q", f.Category)
	}
	if len(f.Evidence) == 0 {
		t.Error("no evidence")
	}
}

func TestFailedScheduling_InsufficientMemory(t *testing.T) {
	f := rules.CheckFailedScheduling(pendingPodWithEvent("0/3 nodes are available: insufficient memory"))
	if f == nil {
		t.Fatal("expected finding")
	}
}

func TestFailedScheduling_Taints(t *testing.T) {
	f := rules.CheckFailedScheduling(pendingPodWithEvent("node(s) had taint {dedicated: gpu}, that the pod didn't tolerate"))
	if f == nil {
		t.Fatal("expected finding for taint")
	}
}

func TestFailedScheduling_NotPending(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		Events: []corev1.Event{
			{Reason: "FailedScheduling", Message: "insufficient cpu"},
		},
	}
	if f := rules.CheckFailedScheduling(facts); f != nil {
		t.Error("should not fire for running pod")
	}
}

func TestFailedScheduling_NoEvents(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
	}
	if f := rules.CheckFailedScheduling(facts); f != nil {
		t.Error("should not fire when no FailedScheduling events")
	}
}

func TestFailedScheduling_NilPod(t *testing.T) {
	if f := rules.CheckFailedScheduling(&collect.PodFacts{}); f != nil {
		t.Error("expected nil for nil pod")
	}
}

func TestFailedScheduling_NodeCondition(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
		Events: []corev1.Event{
			{Reason: "FailedScheduling", Message: "node memory pressure"},
		},
		NodeConditions: []corev1.NodeCondition{
			{
				Type:    corev1.NodeMemoryPressure,
				Status:  corev1.ConditionTrue,
				Message: "Node is under memory pressure",
			},
		},
	}
	f := rules.CheckFailedScheduling(facts)
	if f == nil {
		t.Fatal("expected finding with node conditions")
	}
	found := false
	for _, e := range f.Evidence {
		if len(e) > 0 && contains(e, "MemoryPressure") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MemoryPressure in evidence, got %v", f.Evidence)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
