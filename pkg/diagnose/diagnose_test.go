// SPDX-License-Identifier: MIT

package diagnose_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/diagnose"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/target"
)

var labels = map[string]string{"app": "web"}

func crashingPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", RestartCount: 3,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "CreateContainerConfigError",
					Message: `secret "db-creds" not found`,
				}},
			}},
		},
	}
}

func run(t *testing.T, arg string, objs ...runtime.Object) *diagnose.Result {
	t.Helper()
	res, err := diagnose.Run(context.Background(), fake.NewSimpleClientset(objs...), "shop", arg)
	if err != nil {
		t.Fatalf("Run(%q): %v", arg, err)
	}
	return res
}

func categories(findings []rules.Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Category)
	}
	return out
}

func TestRun_Pod(t *testing.T) {
	res := run(t, "web-1", crashingPod("web-1"))
	if res.Target.Pod != "web-1" || !res.Target.PodUnhealthy || res.Facts == nil {
		t.Errorf("unexpected target/facts: %+v", res.Target)
	}
	if got := categories(res.Findings); len(got) != 1 || got[0] != rules.CategoryMissingSecret {
		t.Errorf("findings = %v, want [MissingSecret]", got)
	}
	if res.Findings[0].Evidence[0].ID != "E1" {
		t.Errorf("evidence IDs not assigned: %+v", res.Findings[0].Evidence[0])
	}
}

// A Service that selects nothing: Service finding, no pod, evidence numbered.
func TestRun_ServiceSelectingNothing(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "frontend"}},
	}
	res := run(t, "svc/web", svc, crashingPod("web-1"))
	if res.Target.Kind != target.KindService || res.Target.Pod != "" || res.Facts != nil {
		t.Errorf("unexpected result: target=%+v facts=%v", res.Target, res.Facts != nil)
	}
	if got := categories(res.Findings); len(got) != 1 || got[0] != rules.CategorySelectorMismatch {
		t.Errorf("findings = %v, want [SelectorMismatch]", got)
	}
}

// Deployment + its pod: Deployment findings first, then the pod's, with one
// E1…En sequence across both.
func TestRun_DeploymentMergesFindingsAndNumbersEvidence(t *testing.T) {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}},
		Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue,
			Reason: "FailedCreate", Message: "exceeded quota: pods",
		}}},
	}
	res := run(t, "deploy/web", d, crashingPod("web-1"))
	got := categories(res.Findings)
	if len(got) != 2 || got[0] != rules.CategoryReplicaFailure || got[1] != rules.CategoryMissingSecret {
		t.Fatalf("findings = %v, want [ReplicaFailure MissingSecret]", got)
	}
	n := 0
	for _, f := range res.Findings {
		for _, e := range f.Evidence {
			n++
			if want := "E" + strconv.Itoa(n); e.ID != want {
				t.Errorf("evidence #%d has ID %q, want %q", n, e.ID, want)
			}
		}
	}
}

func TestRun_Errors(t *testing.T) {
	if _, err := diagnose.Run(context.Background(), fake.NewSimpleClientset(), "shop", "ghost"); err == nil {
		t.Error("expected error for missing pod")
	}
	if _, err := diagnose.Run(context.Background(), fake.NewSimpleClientset(), "shop", "ingress/x"); err == nil {
		t.Error("expected error for unsupported kind")
	}
}

func TestRedacted(t *testing.T) {
	pod := crashingPod("web-1")
	pod.Status.ContainerStatuses[0].State.Waiting.Message = `secret "db-creds" not found; DB_PASSWORD=hunter2hunter2`
	res := run(t, "web-1", pod)
	_, redacted := res.Redacted()
	for _, f := range redacted {
		for _, e := range f.Evidence {
			if strings.Contains(e.Value, "hunter2") {
				t.Errorf("secret not redacted: %q", e.Value)
			}
		}
	}
	// The raw result (for the local terminal) is untouched.
	found := false
	for _, e := range res.Findings[0].Evidence {
		found = found || strings.Contains(e.Value, "hunter2")
	}
	if !found {
		t.Error("Redacted must not modify the raw findings")
	}
}
