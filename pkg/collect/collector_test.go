// SPDX-License-Identifier: MIT

package collect_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

func makePod(name, ns string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "nginx:latest"},
			},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestCollect_BasicPod(t *testing.T) {
	pod := makePod("mypod", "default", corev1.PodRunning)
	client := fake.NewSimpleClientset(pod)

	c := collect.New(client)
	facts, err := c.Collect(context.Background(), "mypod", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if facts.Pod == nil {
		t.Fatal("expected Pod to be set")
	}
	if facts.Pod.Name != "mypod" {
		t.Errorf("got pod name %q, want %q", facts.Pod.Name, "mypod")
	}
}

func TestCollect_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	c := collect.New(client)
	_, err := c.Collect(context.Background(), "ghost", "default")
	if err == nil {
		t.Fatal("expected error for missing pod, got nil")
	}
}

func TestCollect_Events(t *testing.T) {
	pod := makePod("evtpod", "default", corev1.PodPending)
	evt := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "default"},
		InvolvedObject: corev1.ObjectReference{
			Kind: "Pod", Name: "evtpod", Namespace: "default",
		},
		Reason:  "FailedScheduling",
		Message: "0/1 nodes are available: insufficient cpu",
	}
	client := fake.NewSimpleClientset(pod, evt)
	c := collect.New(client)
	facts, err := c.Collect(context.Background(), "evtpod", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts.Events) == 0 {
		t.Error("expected at least one event")
	}
}

func TestCollect_ConfigMapSecretNames(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "refpod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "cfg",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: "my-config"},
						},
					},
				},
				{
					Name: "sec",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: "my-secret"},
					},
				},
			},
			Containers: []corev1.Container{{Name: "app", Image: "nginx"}},
		},
	}
	client := fake.NewSimpleClientset(pod)
	c := collect.New(client)
	facts, err := c.Collect(context.Background(), "refpod", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts.RelatedConfigMaps) != 1 || facts.RelatedConfigMaps[0] != "my-config" {
		t.Errorf("got ConfigMaps %v, want [my-config]", facts.RelatedConfigMaps)
	}
	if len(facts.RelatedSecrets) != 1 || facts.RelatedSecrets[0] != "my-secret" {
		t.Errorf("got Secrets %v, want [my-secret]", facts.RelatedSecrets)
	}
}

func TestCollect_PVC(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-pvc", Namespace: "default"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pvcpod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "data-pvc",
					},
				},
			}},
			Containers: []corev1.Container{{Name: "app", Image: "nginx"}},
		},
	}
	client := fake.NewSimpleClientset(pod, pvc)
	c := collect.New(client)
	facts, err := c.Collect(context.Background(), "pvcpod", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts.PVCs) != 1 {
		t.Errorf("expected 1 PVC, got %d", len(facts.PVCs))
	}
}
