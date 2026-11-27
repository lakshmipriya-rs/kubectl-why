// SPDX-License-Identifier: MIT

package rules_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func strPtr(s string) *string { return &s }

// pvcPod mounts claim "data" as volume "storage".
func pvcPod(nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "db-0"},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Volumes: []corev1.Volume{{
				Name: "storage",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
}

func claim(phase corev1.PersistentVolumeClaimPhase, class *string) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: class,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: phase},
	}
}

func pvcFacts(nodeName string, pvc corev1.PersistentVolumeClaim, events ...corev1.Event) *collect.PodFacts {
	return &collect.PodFacts{
		Pod:       pvcPod(nodeName),
		PVCs:      []corev1.PersistentVolumeClaim{pvc},
		PVCEvents: map[string][]corev1.Event{"data": events},
	}
}

func TestUnboundPVC_UnknownStorageClass(t *testing.T) {
	f := rules.CheckUnboundPVC(pvcFacts("", claim(corev1.ClaimPending, strPtr("fast")), corev1.Event{
		Reason: "ProvisioningFailed", Message: `storageclass.storage.k8s.io "fast" not found`, Count: 7,
	}))
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryUnboundPVC || f.Severity != "critical" {
		t.Errorf("category/severity = %s/%s", f.Category, f.Severity)
	}
	for _, want := range []string{
		"Pod/db-0 spec.volumes[storage].persistentVolumeClaim.claimName: data",
		"PersistentVolumeClaim/data status.phase: Pending",
		"PersistentVolumeClaim/data spec.storageClassName: fast",
		"PersistentVolumeClaim/data spec.resources.requests.storage: 1Gi",
		`PersistentVolumeClaim/data event ProvisioningFailed: storageclass.storage.k8s.io "fast" not found (x7)`,
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	if !strings.Contains(f.Detail, `StorageClass "fast" does not exist`) {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
}

func TestUnboundPVC_NoDefaultStorageClass(t *testing.T) {
	f := rules.CheckUnboundPVC(pvcFacts("", claim(corev1.ClaimPending, nil), corev1.Event{
		Reason: "FailedBinding", Message: "no persistent volumes available for this claim and no storage class is set",
	}))
	if f == nil {
		t.Fatal("expected finding")
	}
	if !strings.Contains(f.Detail, "no default StorageClass") {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
	if !evidenceContains(f.Evidence, "spec.storageClassName: (not set: cluster default)") {
		t.Errorf("missing storage class evidence: %v", f.Evidence)
	}
}

func TestUnboundPVC_ProvisioningFailedGeneric(t *testing.T) {
	f := rules.CheckUnboundPVC(pvcFacts("node-1", claim(corev1.ClaimPending, strPtr("ebs")), corev1.Event{
		Reason: "ProvisioningFailed", Message: "failed to provision volume: quota exceeded",
	}))
	if f == nil || !strings.Contains(f.Detail, "Provisioning the volume failed: failed to provision volume: quota exceeded") {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

func TestUnboundPVC_MissingClaim(t *testing.T) {
	facts := &collect.PodFacts{Pod: pvcPod(""), MissingPVCs: []string{"data"}}
	f := rules.CheckUnboundPVC(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if !strings.Contains(f.Detail, `PersistentVolumeClaim "data" does not exist`) {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
	if !evidenceContains(f.Evidence, "spec.volumes[storage].persistentVolumeClaim.claimName: data") {
		t.Errorf("missing claim reference: %v", f.Evidence)
	}
}

func TestUnboundPVC_Lost(t *testing.T) {
	f := rules.CheckUnboundPVC(pvcFacts("node-1", claim(corev1.ClaimLost, strPtr("standard"))))
	if f == nil || !strings.Contains(f.Detail, "is Lost") {
		t.Fatalf("expected Lost finding, got %+v", f)
	}
}

// WaitForFirstConsumer: Pending is expected until the pod is scheduled.
func TestUnboundPVC_WaitForFirstConsumer(t *testing.T) {
	waiting := corev1.Event{Reason: "WaitForFirstConsumer", Message: "waiting for first consumer to be created before binding"}

	if f := rules.CheckUnboundPVC(pvcFacts("", claim(corev1.ClaimPending, strPtr("standard")), waiting)); f != nil {
		t.Errorf("unscheduled pod: PVC waiting for consumer is not the cause, got %+v", f)
	}

	// Once the pod is on a node, a claim still waiting is a real problem.
	f := rules.CheckUnboundPVC(pvcFacts("node-1", claim(corev1.ClaimPending, strPtr("standard")), waiting))
	if f == nil || !strings.Contains(f.Detail, "binds on first use") {
		t.Errorf("scheduled pod: expected finding, got %+v", f)
	}
}

func TestUnboundPVC_NotTriggered(t *testing.T) {
	for name, facts := range map[string]*collect.PodFacts{
		"nil pod":   {},
		"no claims": {Pod: pvcPod("node-1")},
		"bound":     pvcFacts("node-1", claim(corev1.ClaimBound, strPtr("standard"))),
	} {
		t.Run(name, func(t *testing.T) {
			if f := rules.CheckUnboundPVC(facts); f != nil {
				t.Errorf("expected nil, got %+v", f)
			}
		})
	}
}
