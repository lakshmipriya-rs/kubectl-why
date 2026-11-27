// SPDX-License-Identifier: MIT

package target

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParse(t *testing.T) {
	tests := []struct {
		arg      string
		wantKind Kind
		wantName string
		wantErr  bool
	}{
		{"web-1", KindPod, "web-1", false},
		{"pod/web-1", KindPod, "web-1", false},
		{"po/web-1", KindPod, "web-1", false},
		{"deploy/web", KindDeployment, "web", false},
		{"Deployment/web", KindDeployment, "web", false},
		{"deployment.apps/web", KindDeployment, "web", false},
		{"sts/db", KindStatefulSet, "db", false},
		{"ds/agent", KindDaemonSet, "agent", false},
		{"rs/web-abc", KindReplicaSet, "web-abc", false},
		{"job/migrate", KindJob, "migrate", false},
		{"jobs.batch/migrate", KindJob, "migrate", false},
		{"svc/web", "", "", true},
		{"deploy/", "", "", true},
		{"deploy/a/b", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			kind, name, err := Parse(tc.arg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if kind != tc.wantKind || name != tc.wantName {
				t.Errorf("got %s/%s, want %s/%s", kind, name, tc.wantKind, tc.wantName)
			}
		})
	}
}

// ── Pod fixtures ─────────────────────────────────────────────────────────────

var created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func pod(name string, labels map[string]string, age time.Duration, status corev1.PodStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "ns", Labels: labels,
			CreationTimestamp: metav1.NewTime(created.Add(-age)),
		},
		Status: status,
	}
}

func running(restarts int32) corev1.PodStatus {
	return corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "app", Ready: true, RestartCount: restarts,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}}
}

func waiting(reason string) corev1.PodStatus {
	return corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "app", RestartCount: 4,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
	}}}
}

var webLabels = map[string]string{"app": "web"}

func deployment(name string, labels map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}},
	}
}

func resolve(t *testing.T, arg string, objs ...runtime.Object) (Target, error) {
	t.Helper()
	return Resolve(context.Background(), fake.NewSimpleClientset(objs...), "ns", arg)
}

// ── Resolve ──────────────────────────────────────────────────────────────────

func TestResolve_PodNeedsNoLookup(t *testing.T) {
	got, err := resolve(t, "web-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != "web-1" || got.IsWorkload() {
		t.Errorf("unexpected target %+v", got)
	}
}

func TestResolve_DeploymentPicksUnhealthiestPod(t *testing.T) {
	got, err := resolve(t, "deploy/web",
		deployment("web", webLabels),
		pod("web-healthy", webLabels, time.Hour, running(0)),
		pod("web-crashing", webLabels, 2*time.Hour, waiting("CrashLoopBackOff")),
		pod("web-restarted", webLabels, time.Hour, running(2)),
		// Same namespace, different app: must be ignored even though it's failing.
		pod("other-broken", map[string]string{"app": "other"}, time.Hour, waiting("ImagePullBackOff")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != "web-crashing" {
		t.Errorf("chose %q, want web-crashing", got.Pod)
	}
	if got.PodsTotal != 3 || got.PodsUnhealthy != 2 {
		t.Errorf("pods total/unhealthy = %d/%d, want 3/2", got.PodsTotal, got.PodsUnhealthy)
	}
	if got.Kind != KindDeployment || got.Name != "web" {
		t.Errorf("unexpected target %+v", got)
	}
}

func TestResolve_AllHealthyPicksMostRestartedThenNewest(t *testing.T) {
	got, err := resolve(t, "deploy/web",
		deployment("web", webLabels),
		pod("web-old", webLabels, 3*time.Hour, running(0)),
		pod("web-new", webLabels, time.Minute, running(0)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != "web-new" || got.PodsUnhealthy != 0 {
		t.Errorf("got %+v, want newest pod web-new with 0 unhealthy", got)
	}
}

func TestResolve_WorkloadWithNoPods(t *testing.T) {
	_, err := resolve(t, "deploy/web", deployment("web", webLabels))
	if err == nil || !strings.Contains(err.Error(), "has no pods") {
		t.Fatalf("expected 'has no pods' error, got %v", err)
	}
}

func TestResolve_WorkloadNotFound(t *testing.T) {
	_, err := resolve(t, "sts/missing")
	if err == nil || !strings.Contains(err.Error(), "StatefulSet/missing") {
		t.Fatalf("expected not-found error naming the workload, got %v", err)
	}
}

func TestResolve_Job(t *testing.T) {
	labels := map[string]string{"job-name": "migrate"}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "ns"},
		Spec:       batchv1.JobSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}},
	}
	failed := pod("migrate-x1", labels, time.Hour, corev1.PodStatus{Phase: corev1.PodFailed})
	ok := pod("migrate-x2", labels, time.Minute, corev1.PodStatus{Phase: corev1.PodSucceeded})
	got, err := resolve(t, "job/migrate", job, failed, ok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pod != "migrate-x1" || got.PodsUnhealthy != 1 {
		t.Errorf("got %+v, want failed pod migrate-x1", got)
	}
}

func TestUnhealthiness(t *testing.T) {
	tests := []struct {
		name   string
		status corev1.PodStatus
		want   int
	}{
		{"healthy", running(0), 0},
		{"restarted but running", running(3), 1},
		{"crash loop", waiting("CrashLoopBackOff"), 3},
		{"still creating is not unhealthy", corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
		}}}, 0},
		{"pending", corev1.PodStatus{Phase: corev1.PodPending}, 3},
		{"failed", corev1.PodStatus{Phase: corev1.PodFailed}, 4},
		{"succeeded", corev1.PodStatus{Phase: corev1.PodSucceeded}, 0},
		{"running but not ready", corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unhealthiness(corev1.Pod{Status: tc.status}); got != tc.want {
				t.Errorf("unhealthiness = %d, want %d", got, tc.want)
			}
		})
	}
}
