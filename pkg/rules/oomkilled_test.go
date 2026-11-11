// SPDX-License-Identifier: MIT

package rules_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func podWithOOM(exitCode int32, reason string) *collect.PodFacts {
	return &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "oom-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "app",
						Image: "stress:latest",
						Resources: corev1.ResourceRequirements{
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("10Mi"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "app",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: exitCode,
								Reason:   reason,
							},
						},
					},
				},
			},
		},
	}
}

func TestOOMKilled_ExitCode137(t *testing.T) {
	f := rules.CheckOOMKilled(podWithOOM(137, "Error"))
	if f == nil {
		t.Fatal("expected finding for exit code 137")
	}
	if f.Category != rules.CategoryOOMKilled {
		t.Errorf("category = %q, want %q", f.Category, rules.CategoryOOMKilled)
	}
}

func TestOOMKilled_ReasonOOMKilled(t *testing.T) {
	f := rules.CheckOOMKilled(podWithOOM(137, "OOMKilled"))
	if f == nil {
		t.Fatal("expected finding for OOMKilled reason")
	}
}

func TestOOMKilled_LastTerminationState(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "crash-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "stress"}},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "app",
						State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 137,
								Reason:   "OOMKilled",
							},
						},
					},
				},
			},
		},
	}
	f := rules.CheckOOMKilled(facts)
	if f == nil {
		t.Fatal("expected finding from LastTerminationState")
	}
}

func TestOOMKilled_NotDetected_CleanExit(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "ok-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "nginx"}},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "app",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		},
	}
	if f := rules.CheckOOMKilled(facts); f != nil {
		t.Errorf("expected nil for running container, got %+v", f)
	}
}
