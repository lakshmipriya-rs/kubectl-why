// SPDX-License-Identifier: MIT

package rules_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func service(selector map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"},
		Spec:       corev1.ServiceSpec{Selector: selector, Ports: ports},
	}
}

func svcPod(name string, labels map[string]string, ready bool, ports ...corev1.ContainerPort) corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Ports: ports}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

var webSelector = map[string]string{"app": "web", "tier": "frontend"}

func TestSelectorMismatch_NearMiss(t *testing.T) {
	sf := &collect.ServiceFacts{
		Service: service(webSelector),
		Pods: []corev1.Pod{
			svcPod("web-abc", map[string]string{"app": "web", "tier": "front"}, true),
			svcPod("api-xyz", map[string]string{"app": "api"}, true),
		},
	}
	f := rules.CheckSelectorMismatch(sf)
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategorySelectorMismatch || f.Severity != "critical" {
		t.Errorf("category/severity = %s/%s", f.Category, f.Severity)
	}
	for _, want := range []string{
		"Service/web spec.selector: app=web, tier=frontend",
		`Service/web selected pods: 0 of 2 pods in namespace "shop"`,
		"Pod/web-abc metadata.labels (selector keys): app=web, tier=front",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	if evidenceContains(f.Evidence, "Pod/api-xyz") {
		t.Errorf("pod sharing no selector label should not be listed: %v", f.Evidence)
	}
	if !strings.Contains(f.Detail, `Closest match: pod "web-abc", which has tier=front (selector wants tier=frontend)`) {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
}

func TestSelectorMismatch_MissingLabelAndNoNearMiss(t *testing.T) {
	sf := &collect.ServiceFacts{
		Service: service(webSelector),
		Pods:    []corev1.Pod{svcPod("web-abc", map[string]string{"app": "web"}, true)},
	}
	f := rules.CheckSelectorMismatch(sf)
	if f == nil || !strings.Contains(f.Detail, `has no "tier" label`) {
		t.Fatalf("expected missing-label detail, got %+v", f)
	}

	sf.Pods = []corev1.Pod{svcPod("db-0", map[string]string{"app": "db"}, true)}
	f = rules.CheckSelectorMismatch(sf)
	if f == nil || !strings.Contains(f.Detail, "No pod has any of these labels") {
		t.Fatalf("expected no-near-miss detail, got %+v", f)
	}
}

func TestSelectorMismatch_IgnoresFinishedPods(t *testing.T) {
	done := svcPod("web-job", webSelector, false)
	done.Status.Phase = corev1.PodSucceeded
	sf := &collect.ServiceFacts{Service: service(webSelector), Pods: []corev1.Pod{done}}
	if f := rules.CheckSelectorMismatch(sf); f == nil {
		t.Error("a Succeeded pod can't be an endpoint; the selector effectively matches nothing")
	}
}

func TestNoReadyEndpoints(t *testing.T) {
	sf := &collect.ServiceFacts{
		Service: service(webSelector),
		Pods: []corev1.Pod{
			svcPod("web-1", webSelector, false),
			svcPod("web-2", webSelector, false),
		},
	}
	f := rules.CheckNoReadyEndpoints(sf)
	if f == nil {
		t.Fatal("expected finding")
	}
	if !evidenceContains(f.Evidence, "Service/web selected pods: 2 selected, 0 ready") ||
		!evidenceContains(f.Evidence, "Pod/web-1 status.conditions[Ready]: False (phase Running)") {
		t.Errorf("unexpected evidence: %v", f.Evidence)
	}
	if rules.CheckSelectorMismatch(sf) != nil {
		t.Error("selector matches pods; SelectorMismatch must not fire")
	}

	sf.Pods = append(sf.Pods, svcPod("web-3", webSelector, true))
	if f := rules.CheckNoReadyEndpoints(sf); f != nil {
		t.Errorf("one ready pod is enough for endpoints, got %+v", f)
	}
}

func TestServicePortMismatch(t *testing.T) {
	http := corev1.ContainerPort{Name: "http", ContainerPort: 8080}
	tests := []struct {
		name     string
		port     corev1.ServicePort
		ports    []corev1.ContainerPort
		wantHint string // empty: no finding expected
	}{
		{"matching number", corev1.ServicePort{Port: 80, TargetPort: intstr.FromInt32(8080)}, []corev1.ContainerPort{http}, ""},
		{"matching name", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("http")}, []corev1.ContainerPort{http}, ""},
		{"unset targetPort defaults to port", corev1.ServicePort{Port: 8080}, []corev1.ContainerPort{http}, ""},
		{"wrong number", corev1.ServicePort{Name: "web", Port: 80, TargetPort: intstr.FromInt32(8081)}, []corev1.ContainerPort{http},
			"port 80 targets container port 8081, which the pods don't declare (they declare 8080 (http))"},
		{"unknown name", corev1.ServicePort{Port: 80, TargetPort: intstr.FromString("metrics")}, []corev1.ContainerPort{http},
			`targets the named port "metrics"`},
		{"pods declare no ports", corev1.ServicePort{Port: 80, TargetPort: intstr.FromInt32(9999)}, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sf := &collect.ServiceFacts{
				Service: service(webSelector, tc.port),
				Pods:    []corev1.Pod{svcPod("web-1", webSelector, true, tc.ports...)},
			}
			f := rules.CheckServicePortMismatch(sf)
			if tc.wantHint == "" {
				if f != nil {
					t.Errorf("expected no finding, got %+v", f)
				}
				return
			}
			if f == nil || !strings.Contains(f.Detail, tc.wantHint) {
				t.Fatalf("expected detail containing %q, got %+v", tc.wantHint, f)
			}
		})
	}
}

func TestRunServiceRules_NoSelectorIsQuiet(t *testing.T) {
	sf := &collect.ServiceFacts{Service: service(nil, corev1.ServicePort{Port: 80})}
	if got := rules.RunServiceRules(sf); len(got) != 0 {
		t.Errorf("Service without selector (manual endpoints) should produce no findings, got %+v", got)
	}
}
