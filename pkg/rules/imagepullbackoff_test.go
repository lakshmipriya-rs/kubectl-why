// SPDX-License-Identifier: MIT

package rules_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func fakePodWithWaiting(reason string) *collect.PodFacts {
	return &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-img", Namespace: "default"},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "app",
						Image: "nginx:doesnotexist999",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason:  reason,
								Message: "Back-off pulling image",
							},
						},
					},
				},
			},
		},
	}
}

func TestImagePullBackOff_Detected(t *testing.T) {
	for _, reason := range []string{"ImagePullBackOff", "ErrImagePull"} {
		t.Run(reason, func(t *testing.T) {
			facts := fakePodWithWaiting(reason)
			f := rules.CheckImagePullBackOff(facts)
			if f == nil {
				t.Fatalf("expected finding for reason %q, got nil", reason)
			}
			if f.Category != rules.CategoryImagePullBackOff {
				t.Errorf("category = %q, want %q", f.Category, rules.CategoryImagePullBackOff)
			}
			if f.Severity != "critical" {
				t.Errorf("severity = %q, want critical", f.Severity)
			}
			if len(f.Evidence) == 0 {
				t.Error("expected at least one evidence item")
			}
		})
	}
}

func TestImagePullBackOff_NotDetected(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "app",
						Image: "nginx:latest",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		},
	}
	if f := rules.CheckImagePullBackOff(facts); f != nil {
		t.Errorf("expected nil finding for running container, got %+v", f)
	}
}

func TestImagePullBackOff_NilPod(t *testing.T) {
	if f := rules.CheckImagePullBackOff(&collect.PodFacts{}); f != nil {
		t.Error("expected nil for nil pod")
	}
}

func TestImagePullBackOff_InitContainer(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "init",
						Image: "badrepo/init:missing",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"},
						},
					},
				},
			},
		},
	}
	f := rules.CheckImagePullBackOff(facts)
	if f == nil {
		t.Fatal("expected finding for init container pull error")
	}
}
