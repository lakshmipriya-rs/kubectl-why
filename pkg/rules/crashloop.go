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
			if cs.State.Waiting == nil || cs.State.Waiting.Reason != "CrashLoopBackOff" {
				continue
			}
			f := statusField(init, cs.Name)
			evidence = append(evidence,
				Evidence{Source: src, Field: f + ".state.waiting.reason", Value: "CrashLoopBackOff"},
				Evidence{Source: src, Field: f + ".restartCount", Value: strconv.Itoa(int(cs.RestartCount))},
			)

			last := cs.LastTerminationState.Terminated
			if last != nil {
				t := termination{init: init, container: cs.Name, last: true, state: last}
				evidence = append(evidence, t.evidence(src)...)
			}

			logKey := cs.Name
			if init {
				logKey = collect.InitLogKey(cs.Name)
			}
			logLines := tailLines(facts.PreviousLogs[logKey], crashLogLines)
			for _, line := range logLines {
				evidence = append(evidence, Evidence{
					Source: src,
					Field:  fmt.Sprintf("logs[%s].previous", cs.Name),
					Value:  line,
				})
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
