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

// Node conditions are collected whenever the pod is assigned to a node, not
// only while Pending (e.g. a Running pod on a node under MemoryPressure).
func TestCollect_NodeConditions_ScheduledPod(t *testing.T) {
	pod := makePod("onnode", "default", corev1.PodRunning)
	pod.Spec.NodeName = "worker-1"
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
			},
		},
	}
	client := fake.NewSimpleClientset(pod, node)
	facts, err := collect.New(client).Collect(context.Background(), "onnode", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts.NodeConditions) != 1 || facts.NodeConditions[0].Type != corev1.NodeMemoryPressure {
		t.Errorf("expected MemoryPressure condition, got %v", facts.NodeConditions)
	}
}

func TestCollect_NodeConditions_UnscheduledPod(t *testing.T) {
	pod := makePod("nonode", "default", corev1.PodPending)
	client := fake.NewSimpleClientset(pod)
	facts, err := collect.New(client).Collect(context.Background(), "nonode", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts.NodeConditions) != 0 {
		t.Errorf("expected no node conditions for unscheduled pod, got %v", facts.NodeConditions)
	}
}

// Init containers get both current and previous logs, under "init:<name>".
func TestCollect_InitContainerLogs(t *testing.T) {
	pod := makePod("initpod", "default", corev1.PodPending)
	pod.Spec.InitContainers = []corev1.Container{{Name: "migrate", Image: "busybox"}}
	client := fake.NewSimpleClientset(pod)
	facts, err := collect.New(client).Collect(context.Background(), "initpod", "default")
	if err != nil {
		t.Fatal(err)
	}
	key := collect.InitLogKey("migrate")
	if _, ok := facts.CurrentLogs[key]; !ok {
		t.Errorf("missing current logs for %q: %v", key, facts.CurrentLogs)
	}
	if _, ok := facts.PreviousLogs[key]; !ok {
		t.Errorf("missing previous logs for %q: %v", key, facts.PreviousLogs)
	}
}
