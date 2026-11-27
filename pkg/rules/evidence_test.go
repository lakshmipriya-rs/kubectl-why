// SPDX-License-Identifier: MIT

package rules_test

import (
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

// Evidence must point at the exact object and field it came from.
func TestImagePullBackOff_EvidenceIsStructured(t *testing.T) {
	facts := fakePodWithWaiting("ImagePullBackOff")
	facts.Events = []corev1.Event{
		{Reason: "Failed", Message: `Failed to pull image "nginx:doesnotexist999"`, Count: 3},
	}
	f := rules.CheckImagePullBackOff(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	want := []rules.Evidence{
		{Source: "Pod/bad-img", Field: "status.containerStatuses[app].state.waiting.reason", Value: "ImagePullBackOff"},
		{Source: "Pod/bad-img", Field: "status.containerStatuses[app].image", Value: "nginx:doesnotexist999"},
		{Source: "Pod/bad-img", Field: "status.containerStatuses[app].state.waiting.message", Value: "Back-off pulling image"},
		{Source: "Event", Field: "Failed", Value: `Failed to pull image "nginx:doesnotexist999" (x3)`},
	}
	if len(f.Evidence) != len(want) {
		t.Fatalf("got %d evidence items, want %d: %v", len(f.Evidence), len(want), f.Evidence)
	}
	for i := range want {
		if f.Evidence[i] != want[i] {
			t.Errorf("evidence[%d] = %+v, want %+v", i, f.Evidence[i], want[i])
		}
	}
}

// RunAll numbers evidence E1..En across all findings, with no duplicates.
func TestRunAll_AssignsUniqueEvidenceIDs(t *testing.T) {
	// A pod that is both OOM-killed (last run) and stuck pulling its image now
	// produces two findings.
	facts := podWithOOM(137, "OOMKilled")
	cs := &facts.Pod.Status.ContainerStatuses[0]
	cs.LastTerminationState = cs.State
	cs.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}

	findings := rules.RunAll(facts)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}
	seen := map[string]bool{}
	n := 0
	for _, f := range findings {
		for _, e := range f.Evidence {
			n++
			if e.ID == "" || seen[e.ID] {
				t.Errorf("missing or duplicate evidence ID %q", e.ID)
			}
			seen[e.ID] = true
		}
	}
	if !seen["E1"] || !seen["E"+strconv.Itoa(n)] {
		t.Errorf("expected IDs E1..E%d, got %v", n, seen)
	}
}

// An OOM-killed container restarted and OOM-killed again lists its memory
// limit once, not once per termination.
func TestOOMKilled_MemoryLimitListedOnce(t *testing.T) {
	facts := podWithOOM(137, "OOMKilled")
	cs := &facts.Pod.Status.ContainerStatuses[0]
	cs.LastTerminationState = cs.State
	f := rules.CheckOOMKilled(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	limits := 0
	for _, e := range f.Evidence {
		if e.Field == "spec.containers[app].resources.limits.memory" {
			limits++
		}
	}
	if limits != 1 {
		t.Errorf("memory limit listed %d times, want 1: %v", limits, f.Evidence)
	}
	if !evidenceContains(f.Evidence, "lastState.terminated.reason: OOMKilled") {
		t.Errorf("expected lastState evidence, got %v", f.Evidence)
	}
}
