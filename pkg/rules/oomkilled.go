// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// CheckOOMKilled detects containers that were terminated due to out-of-memory
// (exit code 137 or Reason == "OOMKilled"). It checks both current and
// previous (restarted) container state.
func CheckOOMKilled(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}

	var evidence []string

	checkTerminated := func(name string, t *corev1.ContainerStateTerminated, memLimit string) {
		if t == nil {
			return
		}
		if t.Reason == "OOMKilled" || t.ExitCode == 137 {
			evidence = append(evidence,
				fmt.Sprintf("container %q terminated: reason=%q exitCode=%d",
					name, t.Reason, t.ExitCode),
				fmt.Sprintf("container %q memory limit: %s", name, memLimit),
			)
		}
	}

	// Build a map of container name → memory limit for readable evidence.
	memLimits := make(map[string]string)
	for _, c := range facts.Pod.Spec.Containers {
		if lim := c.Resources.Limits.Memory(); lim != nil {
			memLimits[c.Name] = lim.String()
		} else {
			memLimits[c.Name] = "none"
		}
	}

	for _, cs := range facts.Pod.Status.ContainerStatuses {
		// Current state.
		checkTerminated(cs.Name, cs.State.Terminated, memLimits[cs.Name])
		// Last state (captures a recently restarted container).
		checkTerminated(cs.Name, cs.LastTerminationState.Terminated, memLimits[cs.Name])
	}
	for _, cs := range facts.Pod.Status.InitContainerStatuses {
		checkTerminated("init:"+cs.Name, cs.State.Terminated, "")
		checkTerminated("init:"+cs.Name, cs.LastTerminationState.Terminated, "")
	}

	if len(evidence) == 0 {
		return nil
	}

	return &Finding{
		Category: CategoryOOMKilled,
		Detail:   "One or more containers were killed by the Linux OOM killer (exit 137). Increase the memory limit or reduce the application's memory usage.",
		Evidence: evidence,
		Severity: "critical",
	}
}
