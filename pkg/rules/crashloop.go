// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

const (
	// crashLogLines is how many trailing log lines of the previous run are
	// shown as evidence. The full tail is still available to --explain.
	crashLogLines = 5
	// maxLogLineLen truncates very long log lines (e.g. JSON blobs).
	maxLogLineLen = 300
)

// CheckCrashLoopBackOff detects containers that keep crashing and are being
// restarted with back-off. Evidence includes how the last run ended and the
// last lines it logged; the detail explains common exit codes.
//
// Kubernetes reports a crash loop in two ways, depending on version and
// timing: as state.waiting.reason=CrashLoopBackOff (the crashed run is in
// lastState), or, as Kubernetes 1.35 does for most of the back-off, as
// state.terminated with restartCount > 0 (the crashed run is the current
// state). Both are detected.
func CheckCrashLoopBackOff(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}
	src := podSource(facts.Pod)

	var (
		evidence []Evidence
		detail   string
	)
	check := func(init bool, statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			f := statusField(init, cs.Name)
			var (
				last     *corev1.ContainerStateTerminated
				lastIsLS bool // the crashed run is in lastState (waiting) vs state (terminated)
			)
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff":
				last, lastIsLS = cs.LastTerminationState.Terminated, true
				evidence = append(evidence, Evidence{Source: src, Field: f + ".state.waiting.reason", Value: "CrashLoopBackOff"})
			case cs.State.Terminated != nil && cs.RestartCount > 0 && willRestart(facts.Pod, init, cs.State.Terminated):
				last = cs.State.Terminated
			default:
				continue
			}
			evidence = append(evidence, Evidence{Source: src, Field: f + ".restartCount", Value: strconv.Itoa(int(cs.RestartCount))})
			if last != nil {
				t := termination{init: init, container: cs.Name, last: lastIsLS, state: last}
				evidence = append(evidence, t.evidence(src)...)
			}

			// The crashed run's logs: "previous" while waiting for a restart,
			// "current" while the terminated container is still the current one.
			logKey := cs.Name
			if init {
				logKey = collect.InitLogKey(cs.Name)
			}
			logs, logField := facts.PreviousLogs[logKey], fmt.Sprintf("logs[%s].previous", cs.Name)
			if !lastIsLS {
				logs, logField = facts.CurrentLogs[logKey], fmt.Sprintf("logs[%s]", cs.Name)
			}
			logLines := tailLines(logs, crashLogLines)
			for _, line := range logLines {
				evidence = append(evidence, Evidence{Source: src, Field: logField, Value: line})
			}

			// Explain the first crashing container; others are in the evidence.
			if detail == "" {
				detail = crashDetail(last, len(logLines) > 0)
			}
		}
	}
	check(true, facts.Pod.Status.InitContainerStatuses)
	check(false, facts.Pod.Status.ContainerStatuses)

	if len(evidence) == 0 {
		return nil
	}

	return &Finding{
		Category: CategoryCrashLoopBackOff,
		Detail:   detail,
		Evidence: evidence,
		Severity: "critical",
	}
}

// crashDetail explains the crash from how the last run ended.
func crashDetail(last *corev1.ContainerStateTerminated, hasLogs bool) string {
	var sb strings.Builder
	sb.WriteString("A container keeps crashing and Kubernetes is waiting (back-off) before restarting it. ")

	switch {
	case last == nil:
		sb.WriteString("How the last run ended is unknown.")
	case last.Reason == "OOMKilled":
		sb.WriteString("Each run is killed by the OOM killer: see the OOMKilled finding.")
	case last.ExitCode == exitCodeSIGKILL:
		sb.WriteString("Each run is killed with SIGKILL (exit 137): see the SIGKILL finding.")
	case last.ExitCode == 0:
		sb.WriteString("The main process exits successfully (exit 0), but the pod restarts it. " +
			"A long-running container must not exit; for run-to-completion work use a Job.")
	case last.ExitCode == 127:
		sb.WriteString("Exit 127: the command was not found. Check command/args and that the binary exists in the image.")
	case last.ExitCode == 126:
		sb.WriteString("Exit 126: the command cannot be executed. Check its file permissions and that it is not a directory.")
	case last.ExitCode == 139:
		sb.WriteString("Exit 139: the application crashed with a segmentation fault (SIGSEGV).")
	case last.ExitCode == 143:
		sb.WriteString("Exit 143: the process was stopped with SIGTERM.")
	default:
		fmt.Fprintf(&sb, "The application exits with code %d.", last.ExitCode)
	}

	if hasLogs {
		sb.WriteString(" The last lines it logged before exiting are in the evidence.")
	} else {
		sb.WriteString(" The previous run produced no log output.")
	}
	return sb.String()
}

// tailLines returns up to n last non-empty lines of logs, each truncated to
// maxLogLineLen characters.
func tailLines(logs string, n int) []string {
	var lines []string
	for _, l := range strings.Split(logs, "\n") {
		l = strings.TrimRight(l, "\r \t")
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > maxLogLineLen {
			l = string(r[:maxLogLineLen]) + "…" // cut on a character boundary
		}
		lines = append(lines, l)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// willRestart reports whether Kubernetes will restart a container that ended
// with t, given the pod's restartPolicy. Init containers are only re-run
// after a failure.
func willRestart(pod *corev1.Pod, init bool, t *corev1.ContainerStateTerminated) bool {
	switch pod.Spec.RestartPolicy {
	case corev1.RestartPolicyNever:
		return false
	case corev1.RestartPolicyOnFailure:
		return t.ExitCode != 0
	default: // Always (also the default when unset)
		return !init || t.ExitCode != 0
	}
}
