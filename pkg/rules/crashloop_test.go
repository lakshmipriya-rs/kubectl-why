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

// crashingPod returns a pod whose "app" container is in CrashLoopBackOff after
// its last run ended with exitCode/reason, with the given previous logs.
func crashingPod(exitCode int32, reason, prevLogs string) *collect.PodFacts {
	return &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "crashy"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "app",
					RestartCount: 5,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode, Reason: reason},
					},
				}},
			},
		},
		PreviousLogs: map[string]string{"app": prevLogs},
	}
}

func TestCrashLoop_EvidenceAndLogs(t *testing.T) {
	logs := "line 1\nline 2\n\nline 3\nline 4\nline 5\nFATAL: config file /etc/app/config.yaml not found\n"
	f := rules.CheckCrashLoopBackOff(crashingPod(1, "Error", logs))
	if f == nil {
		t.Fatal("expected finding")
	}
	if f.Category != rules.CategoryCrashLoopBackOff || f.Severity != "critical" {
		t.Errorf("unexpected category/severity: %s/%s", f.Category, f.Severity)
	}
	for _, want := range []string{
		"Pod/crashy status.containerStatuses[app].state.waiting.reason: CrashLoopBackOff",
		"Pod/crashy status.containerStatuses[app].restartCount: 5",
		"Pod/crashy status.containerStatuses[app].lastState.terminated.exitCode: 1",
		"Pod/crashy logs[app].previous: FATAL: config file /etc/app/config.yaml not found",
	} {
		if !evidenceContains(f.Evidence, want) {
			t.Errorf("missing evidence %q in %v", want, f.Evidence)
		}
	}
	// Only the last 5 non-empty lines are kept.
	logEvidence := 0
	for _, e := range f.Evidence {
		if e.Field == "logs[app].previous" {
			logEvidence++
		}
	}
	if logEvidence != 5 {
		t.Errorf("got %d log lines, want 5", logEvidence)
	}
	if evidenceContains(f.Evidence, "previous: line 1") {
		t.Error("oldest line should have been dropped")
	}
	if !strings.Contains(f.Detail, "exits with code 1") {
		t.Errorf("detail should explain exit code 1: %q", f.Detail)
	}
}

func TestCrashLoop_ExitCodeHints(t *testing.T) {
	tests := []struct {
		code   int32
		reason string
		want   string
	}{
		{127, "Error", "command was not found"},
		{126, "Error", "cannot be executed"},
		{0, "Completed", "must not exit"},
		{137, "OOMKilled", "OOMKilled finding"},
		{137, "Error", "SIGKILL finding"},
		{139, "Error", "segmentation fault"},
		{2, "Error", "exits with code 2"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			f := rules.CheckCrashLoopBackOff(crashingPod(tc.code, tc.reason, ""))
			if f == nil {
				t.Fatal("expected finding")
			}
			if !strings.Contains(f.Detail, tc.want) {
				t.Errorf("detail %q should contain %q", f.Detail, tc.want)
			}
			if !strings.Contains(f.Detail, "no log output") {
				t.Errorf("detail should mention missing logs: %q", f.Detail)
			}
		})
	}
}

func TestCrashLoop_InitContainerUsesInitLogs(t *testing.T) {
	facts := crashingPod(1, "Error", "")
	facts.Pod.Status.InitContainerStatuses = facts.Pod.Status.ContainerStatuses
	facts.Pod.Status.InitContainerStatuses[0].Name = "migrate"
	facts.Pod.Status.ContainerStatuses = nil
	facts.PreviousLogs = map[string]string{
		collect.InitLogKey("migrate"): "error: database unreachable",
		"migrate":                     "wrong key: must not be used",
	}
	f := rules.CheckCrashLoopBackOff(facts)
	if f == nil {
		t.Fatal("expected finding for crash-looping init container")
	}
	if !evidenceContains(f.Evidence, "status.initContainerStatuses[migrate].state.waiting.reason: CrashLoopBackOff") {
		t.Errorf("missing init container evidence: %v", f.Evidence)
	}
	if !evidenceContains(f.Evidence, "logs[migrate].previous: error: database unreachable") ||
		evidenceContains(f.Evidence, "wrong key") {
		t.Errorf("init container logs not read from the init key: %v", f.Evidence)
	}
}

func TestCrashLoop_NotTriggered(t *testing.T) {
	for name, facts := range map[string]*collect.PodFacts{
		"nil pod":         {},
		"image pull":      fakePodWithWaiting("ImagePullBackOff"),
		"running healthy": {Pod: &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if f := rules.CheckCrashLoopBackOff(facts); f != nil {
				t.Errorf("expected nil, got %+v", f)
			}
		})
	}
}

// A crash-looping OOM container gets both findings, and they agree.
func TestCrashLoop_WithOOMBothFindings(t *testing.T) {
	findings := rules.RunAll(crashingPod(137, "OOMKilled", ""))
	got := map[string]bool{}
	for _, f := range findings {
		got[f.Category] = true
	}
	if !got[rules.CategoryCrashLoopBackOff] || !got[rules.CategoryOOMKilled] {
		t.Errorf("want CrashLoopBackOff and OOMKilled, got %v", got)
	}
	if got[rules.CategoryImagePullBackOff] || got[rules.CategorySIGKILL] {
		t.Errorf("unexpected categories: %v", got)
	}
}

func TestCrashLoop_LongLogLineTruncatedSafely(t *testing.T) {
	long := strings.Repeat("é", 400) // multi-byte characters
	f := rules.CheckCrashLoopBackOff(crashingPod(1, "Error", long))
	if f == nil {
		t.Fatal("expected finding")
	}
	for _, e := range f.Evidence {
		if e.Field != "logs[app].previous" {
			continue
		}
		if n := len([]rune(e.Value)); n != 301 { // 300 + "…"
			t.Errorf("truncated line has %d characters, want 301", n)
		}
		if !strings.HasSuffix(e.Value, "…") || strings.ContainsRune(e.Value, '�') {
			t.Errorf("bad truncation: %q", e.Value[len(e.Value)-10:])
		}
	}
}
