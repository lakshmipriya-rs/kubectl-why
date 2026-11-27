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

func httpProbe(port intstr.IntOrString, path string) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:  corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Port: port, Path: path}},
		PeriodSeconds: 10, TimeoutSeconds: 1, FailureThreshold: 3,
	}
}

func unhealthy(container, message string) corev1.Event {
	return corev1.Event{
		Reason:         "Unhealthy",
		Message:        message,
		Count:          4,
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", FieldPath: "spec.containers{" + container + "}"},
	}
}

// probePod has container "web" declaring port 8080 (named "http").
func probePod(readiness, liveness *corev1.Probe, events ...corev1.Event) *collect.PodFacts {
	return &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-1"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:           "web",
				Ports:          []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
				ReadinessProbe: readiness,
				LivenessProbe:  liveness,
			}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "web", Ready: false,
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				}},
			},
		},
		Events: events,
	}
}

func TestProbe_Readiness404(t *testing.T) {
	facts := probePod(httpProbe(intstr.FromInt32(8080), "/healthz"), nil,
		unhealthy("web", "Readiness probe failed: HTTP probe failed with statuscode: 404"))
	f := rules.CheckProbeFailure(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryProbeFailure || f.Severity != "warning" {
		t.Errorf("category/severity = %s/%s, want ProbeFailure/warning", f.Category, f.Severity)
	}
	for _, want := range []string{
		"Event Unhealthy: Readiness probe failed: HTTP probe failed with statuscode: 404 (x4)",
		"Pod/web-1 spec.containers[web].readinessProbe: httpGet :8080/healthz, initialDelaySeconds=0 periodSeconds=10 timeoutSeconds=1 failureThreshold=3",
		"Pod/web-1 status.containerStatuses[web].ready: false",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	for _, want := range []string{"HTTP 404", "path", "no traffic"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q should mention %q", f.Detail, want)
		}
	}
}

func TestProbe_LivenessIsCritical(t *testing.T) {
	facts := probePod(nil, httpProbe(intstr.FromInt32(8080), "/live"),
		unhealthy("web", "Liveness probe failed: Get \"http://10.0.0.5:8080/live\": dial tcp 10.0.0.5:8080: connect: connection refused"))
	f := rules.CheckProbeFailure(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Severity != "critical" {
		t.Errorf("liveness failure should be critical, got %s", f.Severity)
	}
	if !strings.Contains(f.Detail, "nothing is listening") || !strings.Contains(f.Detail, "restarted") {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
}

func TestProbe_WrongPort(t *testing.T) {
	facts := probePod(httpProbe(intstr.FromInt32(9090), "/healthz"), nil,
		unhealthy("web", "Readiness probe failed: Get \"http://10.0.0.5:9090/healthz\": dial tcp: connect: connection refused"))
	f := rules.CheckProbeFailure(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if !strings.Contains(f.Detail, "targets port 9090, but the container's ports are 8080 (http)") {
		t.Errorf("expected port-mismatch detail, got %q", f.Detail)
	}
	if strings.Contains(f.Detail, "nothing is listening") {
		t.Errorf("port mismatch should replace the generic connection-refused hint: %q", f.Detail)
	}
	if !evidenceContains(f.Evidence, "Pod/web-1 spec.containers[web].ports: 8080 (http)") {
		t.Errorf("missing declared-ports evidence: %v", f.Evidence)
	}
}

func TestProbe_NamedPort(t *testing.T) {
	ok := probePod(httpProbe(intstr.FromString("http"), "/healthz"), nil,
		unhealthy("web", "Readiness probe failed: HTTP probe failed with statuscode: 500"))
	f := rules.CheckProbeFailure(ok)
	if f == nil || strings.Contains(f.Detail, "does not define") {
		t.Fatalf("named port 'http' exists; got %+v", f)
	}
	if !strings.Contains(f.Detail, "5xx") {
		t.Errorf("expected 5xx hint, got %q", f.Detail)
	}

	bad := probePod(httpProbe(intstr.FromString("metrics"), "/healthz"), nil,
		unhealthy("web", "Readiness probe failed: connection refused"))
	f = rules.CheckProbeFailure(bad)
	if f == nil || !strings.Contains(f.Detail, `named port "metrics"`) {
		t.Errorf("expected undefined named-port hint, got %+v", f)
	}
}

func TestProbe_NoDeclaredPortsSkipsPortCheck(t *testing.T) {
	facts := probePod(httpProbe(intstr.FromInt32(9090), "/"), nil,
		unhealthy("web", "Readiness probe failed: context deadline exceeded (Client.Timeout exceeded while awaiting headers)"))
	facts.Pod.Spec.Containers[0].Ports = nil
	f := rules.CheckProbeFailure(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if strings.Contains(f.Detail, "wrong port") || !strings.Contains(f.Detail, "timeoutSeconds") {
		t.Errorf("expected timeout hint only, got %q", f.Detail)
	}
}

func TestProbe_ExecProbeDescribed(t *testing.T) {
	exec := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"cat", "/tmp/healthy"}}}}
	facts := probePod(nil, exec, unhealthy("web", "Liveness probe failed: cat: can't open '/tmp/healthy': No such file or directory"))
	f := rules.CheckProbeFailure(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if !evidenceContains(f.Evidence, `livenessProbe: exec ["cat" "/tmp/healthy"]`) {
		t.Errorf("exec probe not described: %v", f.Evidence)
	}
}

func TestProbe_EventWithoutContainerStillReported(t *testing.T) {
	evt := unhealthy("web", "Startup probe failed: connection refused")
	evt.InvolvedObject.FieldPath = ""
	f := rules.CheckProbeFailure(probePod(nil, nil, evt))
	if f == nil {
		t.Fatal("expected finding")
	}
	if !strings.HasPrefix(f.Detail, "The startup probe fails") {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
}

func TestProbe_NotTriggered(t *testing.T) {
	for name, facts := range map[string]*collect.PodFacts{
		"nil pod":   {},
		"no events": probePod(httpProbe(intstr.FromInt32(8080), "/"), nil),
		"other unhealthy reason": probePod(nil, nil, corev1.Event{
			Reason: "Unhealthy", Message: "something else entirely",
		}),
		"different event": probePod(nil, nil, corev1.Event{Reason: "Pulled", Message: "Readiness probe failed: x"}),
	} {
		t.Run(name, func(t *testing.T) {
			if f := rules.CheckProbeFailure(facts); f != nil {
				t.Errorf("expected nil, got %+v", f)
			}
		})
	}
}

// Two probe types failing for the same container: one finding, both explained,
// critical because liveness is among them.
func TestProbe_MultipleProbes(t *testing.T) {
	facts := probePod(httpProbe(intstr.FromInt32(8080), "/ready"), httpProbe(intstr.FromInt32(8080), "/live"),
		unhealthy("web", "Readiness probe failed: HTTP probe failed with statuscode: 503"),
		unhealthy("web", "Liveness probe failed: HTTP probe failed with statuscode: 503"),
	)
	f := rules.CheckProbeFailure(facts)
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Severity != "critical" {
		t.Errorf("severity = %s, want critical", f.Severity)
	}
	if !strings.Contains(f.Detail, "readiness probe") || !strings.Contains(f.Detail, "liveness probe") {
		t.Errorf("both probes should be explained: %q", f.Detail)
	}
}
