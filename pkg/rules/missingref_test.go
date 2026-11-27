// SPDX-License-Identifier: MIT

package rules_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

// refPod references ConfigMap "app-config" via envFrom, key DB_HOST of
// ConfigMap "db-config" via env, and Secret "db-creds" via a volume.
func refPod(waitingMsg string, events ...corev1.Event) *collect.PodFacts {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app-1", Namespace: "shop"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name:         "creds",
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "db-creds"}},
			}},
			Containers: []corev1.Container{{
				Name: "app",
				EnvFrom: []corev1.EnvFromSource{{
					ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}},
				}},
				Env: []corev1.EnvVar{{
					Name: "DB_HOST",
					ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "db-config"}, Key: "DB_HOST",
					}},
				}},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	if waitingMsg != "" {
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "CreateContainerConfigError", Message: waitingMsg,
			}},
		}}
	}
	return &collect.PodFacts{Pod: pod, Events: events}
}

func TestMissingConfigMap_EnvFrom(t *testing.T) {
	f := rules.CheckMissingConfigMap(refPod(`configmap "app-config" not found`))
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryMissingConfigMap || f.Severity != "critical" {
		t.Errorf("category/severity = %s/%s", f.Category, f.Severity)
	}
	for _, want := range []string{
		"Pod/app-1 status.containerStatuses[app].state.waiting.reason: CreateContainerConfigError",
		`Pod/app-1 status.containerStatuses[app].state.waiting.message: configmap "app-config" not found`,
		"Pod/app-1 spec.containers[app].envFrom[0].configMapRef.name: app-config",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	if !strings.Contains(f.Detail, `ConfigMap "app-config" does not exist in namespace "shop"`) {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
	// The Secret rule must not claim this ConfigMap problem.
	if s := rules.CheckMissingSecret(refPod(`configmap "app-config" not found`)); s != nil {
		t.Errorf("MissingSecret fired for a ConfigMap error: %+v", s)
	}
}

func TestMissingConfigMap_MissingKey(t *testing.T) {
	f := rules.CheckMissingConfigMap(refPod("couldn't find key DB_HOST in ConfigMap shop/db-config"))
	if f == nil {
		t.Fatal("expected finding")
	}
	if !strings.Contains(f.Detail, `ConfigMap "db-config" exists but has no key "DB_HOST"`) {
		t.Errorf("unexpected detail: %q", f.Detail)
	}
	if !evidenceContains(f.Evidence, "spec.containers[app].env[DB_HOST].valueFrom.configMapKeyRef: db-config/DB_HOST") {
		t.Errorf("missing env reference: %v", f.Evidence)
	}
	if evidenceContains(f.Evidence, "envFrom") {
		t.Errorf("envFrom of a different ConfigMap should not be listed: %v", f.Evidence)
	}
}

func TestMissingSecret_VolumeFromFailedMountEvent(t *testing.T) {
	evt := corev1.Event{
		Reason:  "FailedMount",
		Message: `MountVolume.SetUp failed for volume "creds" : secret "db-creds" not found`,
		Count:   6,
	}
	f := rules.CheckMissingSecret(refPod("", evt))
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryMissingSecret {
		t.Errorf("category = %s", f.Category)
	}
	for _, want := range []string{
		`Event FailedMount: MountVolume.SetUp failed for volume "creds" : secret "db-creds" not found (x6)`,
		"Pod/app-1 spec.volumes[creds].secret.secretName: db-creds",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	if c := rules.CheckMissingConfigMap(refPod("", evt)); c != nil {
		t.Errorf("MissingConfigMap fired for a Secret error: %+v", c)
	}
}

func TestMissingRef_DuplicateMessagesReportedOnce(t *testing.T) {
	msg := `configmap "app-config" not found`
	f := rules.CheckMissingConfigMap(refPod(msg,
		corev1.Event{Reason: "Failed", Message: "Error: " + msg},
		corev1.Event{Reason: "Failed", Message: "Error: " + msg},
	))
	if f == nil {
		t.Fatal("expected finding")
	}
	if n := strings.Count(f.Detail, `"app-config"`); n != 1 {
		t.Errorf("app-config mentioned %d times in detail, want 1: %q", n, f.Detail)
	}
	refs := 0
	for _, e := range f.Evidence {
		if strings.HasSuffix(e.Field, "configMapRef.name") {
			refs++
		}
	}
	if refs != 1 {
		t.Errorf("spec reference listed %d times, want 1", refs)
	}
}

func TestMissingRef_NotTriggered(t *testing.T) {
	for name, facts := range map[string]*collect.PodFacts{
		"nil pod":       {},
		"no errors":     refPod(""),
		"other waiting": fakePodWithWaiting("ImagePullBackOff"),
		"unrelated mount error": refPod("", corev1.Event{
			Reason: "FailedMount", Message: `MountVolume.SetUp failed for volume "data" : timed out waiting for the condition`,
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if f := rules.CheckMissingConfigMap(facts); f != nil {
				t.Errorf("MissingConfigMap: expected nil, got %+v", f)
			}
			if f := rules.CheckMissingSecret(facts); f != nil {
				t.Errorf("MissingSecret: expected nil, got %+v", f)
			}
		})
	}
}
