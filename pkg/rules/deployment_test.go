// SPDX-License-Identifier: MIT

package rules_test

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func int32Ptr(n int32) *int32 { return &n }

func deploy(replicas int32, revision string, conds ...appsv1.DeploymentCondition) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "shop",
			Annotations: map[string]string{"deployment.kubernetes.io/revision": revision},
		},
		Spec:   appsv1.DeploymentSpec{Replicas: int32Ptr(replicas)},
		Status: appsv1.DeploymentStatus{Conditions: conds},
	}
}

func replicaSet(name, uid, revision string) appsv1.ReplicaSet {
	return appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: name, UID: types.UID(uid),
		Annotations: map[string]string{"deployment.kubernetes.io/revision": revision},
	}}
}

func ownedPod(name, rsUID string, ready bool) corev1.Pod {
	p := svcPod(name, nil, ready)
	p.OwnerReferences = []metav1.OwnerReference{{UID: types.UID(rsUID), Controller: func() *bool { b := true; return &b }()}}
	return p
}

func TestReplicaFailure_Quota(t *testing.T) {
	d := deploy(2, "1")
	d.Status.Replicas, d.Status.ReadyReplicas = 1, 1
	df := &collect.DeploymentFacts{
		Deployment:  d,
		ReplicaSets: []appsv1.ReplicaSet{replicaSet("web-1", "u1", "1")},
		ReplicaSetEvents: map[string][]corev1.Event{"web-1": {{
			Reason:  "FailedCreate",
			Message: `pods "web-1-x" is forbidden: exceeded quota: pod-limit, requested: pods=1, used: pods=1, limited: pods=1`,
			Count:   9,
		}}},
	}
	f := rules.CheckReplicaFailure(df)
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryReplicaFailure || f.Severity != "critical" {
		t.Errorf("category/severity = %s/%s", f.Category, f.Severity)
	}
	if !strings.Contains(f.Detail, "(1 of 2 exist)") || !strings.Contains(f.Detail, `ResourceQuota in namespace "shop"`) {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
	for _, want := range []string{
		"ReplicaSet/web-1 event FailedCreate: pods \"web-1-x\" is forbidden: exceeded quota",
		"(x9)",
		"Deployment/web status: 2 desired, 1 created, 1 ready",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
}

func TestReplicaFailure_Reasons(t *testing.T) {
	tests := map[string]string{
		`pods "x" is forbidden: unable to validate against any security context constraint: [provider "anyuid": Forbidden]`:       "Security Context Constraints",
		`pods "x" is forbidden: violates PodSecurity "restricted:latest": runAsNonRoot != true`:                                   "Pod Security admission",
		`pods "x" is forbidden: maximum cpu usage per Container is 2, but limit is 4`:                                             "LimitRange",
		`pods "x" is forbidden: [minimum memory usage per Container is 64Mi, but request is 16Mi, must be less than or equal to]`: "LimitRange",
		`admission webhook "policy.example.com" denied the request: no latest tags`:                                               "admission webhook",
		`pods "x" is forbidden: error looking up service account shop/builder: serviceaccount "builder" not found`:                "ServiceAccount does not exist",
	}
	for msg, want := range tests {
		t.Run(want, func(t *testing.T) {
			d := deploy(1, "1")
			d.Status.Conditions = []appsv1.DeploymentCondition{{
				Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: msg,
			}}
			f := rules.CheckReplicaFailure(&collect.DeploymentFacts{Deployment: d})
			if f == nil || !strings.Contains(f.Detail, want) {
				t.Fatalf("detail should contain %q, got %+v", want, f)
			}
			if !evidenceContains(f.Evidence, "status.conditions[ReplicaFailure]: True, FailedCreate") {
				t.Errorf("missing condition evidence: %v", f.Evidence)
			}
		})
	}
}

func TestRolloutStuck(t *testing.T) {
	d := deploy(3, "2", appsv1.DeploymentCondition{
		Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse,
		Reason: "ProgressDeadlineExceeded", Message: `ReplicaSet "web-2" has timed out progressing.`,
	})
	d.Spec.ProgressDeadlineSeconds = int32Ptr(120)
	d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Status.AvailableReplicas = 1, 3, 3
	df := &collect.DeploymentFacts{
		Deployment:  d,
		ReplicaSets: []appsv1.ReplicaSet{replicaSet("web-1", "old", "1"), replicaSet("web-2", "new", "2")},
		Pods: []corev1.Pod{
			ownedPod("web-1-a", "old", true), ownedPod("web-1-b", "old", true), ownedPod("web-1-c", "old", true),
			ownedPod("web-2-a", "new", false),
		},
	}
	f := rules.CheckRolloutStuck(df)
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryRolloutStuck {
		t.Errorf("category = %s", f.Category)
	}
	if f.Severity != "warning" {
		t.Errorf("old version still available: severity should be warning, got %s", f.Severity)
	}
	for _, want := range []string{
		`Deployment/web status.conditions[Progressing]: False, ProgressDeadlineExceeded: ReplicaSet "web-2" has timed out progressing.`,
		"Deployment/web status: 3 desired, 1 updated, 3 ready, 3 available",
		"ReplicaSet/web-2 pods: 1, 0 ready (revision 2, new)",
		"ReplicaSet/web-1 pods: 3, 3 ready (revision 1, old)",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	// Newest revision is listed first.
	var rsOrder []string
	for _, e := range f.Evidence {
		if strings.HasPrefix(e.Source, "ReplicaSet/") {
			rsOrder = append(rsOrder, e.Source)
		}
	}
	if strings.Join(rsOrder, ",") != "ReplicaSet/web-2,ReplicaSet/web-1" {
		t.Errorf("ReplicaSet order = %v, want newest first", rsOrder)
	}
	for _, want := range []string{"revision 2 is stuck", "(120s)", "1 of 3 replicas", "still serves 3 ready pod(s)", "kubectl rollout undo deployment/web -n shop"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail should contain %q: %q", want, f.Detail)
		}
	}

	d.Status.AvailableReplicas = 0
	if f := rules.CheckRolloutStuck(df); f == nil || f.Severity != "critical" {
		t.Errorf("nothing available: severity should be critical, got %+v", f)
	}
}

func TestDeploymentRules_NotTriggered(t *testing.T) {
	progressing := appsv1.DeploymentCondition{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: "NewReplicaSetAvailable"}
	healthy := &collect.DeploymentFacts{
		Deployment: deploy(2, "1", progressing),
		ReplicaSetEvents: map[string][]corev1.Event{"web-1": {
			{Reason: "SuccessfulCreate", Message: "Created pod: web-1-a"},
		}},
	}
	if got := rules.RunDeploymentRules(healthy); len(got) != 0 {
		t.Errorf("healthy deployment: expected no findings, got %+v", got)
	}
	if got := rules.RunDeploymentRules(nil); len(got) != 0 {
		t.Errorf("nil facts: expected no findings, got %+v", got)
	}
}

// FailedScheduling now shows what the pod requests when resources are short.
func TestFailedScheduling_ShowsRequests(t *testing.T) {
	facts := pendingPodWithEvent("0/3 nodes are available: 3 Insufficient cpu.")
	facts.Pod.Spec.Containers = []corev1.Container{{
		Name: "app",
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1000"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		}},
	}}
	f := rules.CheckFailedScheduling(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if !evidenceContains(f.Evidence, "Pod/sched-pod spec.containers[app].resources.requests.cpu: 1000") {
		t.Errorf("expected CPU request (as whole cores) in evidence: %v", f.Evidence)
	}
	if evidenceContains(f.Evidence, "requests.memory") {
		t.Errorf("memory isn't short; its request shouldn't be listed: %v", f.Evidence)
	}
}
