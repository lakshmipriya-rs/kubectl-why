// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// exitCodeSIGKILL is 128 + 9: the process was killed with SIGKILL.
const exitCodeSIGKILL = 137

// CheckOOMKilled detects containers terminated by the kernel OOM killer
// (Reason == "OOMKilled"). It checks both current and previous (restarted)
// container state.
//
// Exit code 137 alone is NOT treated as OOM: it only means SIGKILL, which also
// happens after failed liveness probes or an exceeded grace period. That case
// is handled by CheckSIGKILL.
func CheckOOMKilled(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}

	memLimits := memoryLimits(facts.Pod)
	var evidence []string
	forEachTermination(facts.Pod, func(name, key string, t *corev1.ContainerStateTerminated) {
		if t.Reason != "OOMKilled" {
			return
		}
		evidence = append(evidence,
			fmt.Sprintf("container %q terminated: reason=%q exitCode=%d", name, t.Reason, t.ExitCode),
			fmt.Sprintf("container %q memory limit: %s", name, memLimits[key]),
		)
	})

	if len(evidence) == 0 {
		return nil
	}

	return &Finding{
		Category: CategoryOOMKilled,
		Detail:   "One or more containers were killed by the Linux OOM killer. Increase the memory limit or reduce the application's memory usage.",
		Evidence: evidence,
		Severity: "critical",
	}
}

// CheckSIGKILL detects containers that exited with code 137 (SIGKILL) where the
// kernel did NOT report an OOM kill. Typical causes are a failing liveness
// probe, a termination grace period being exceeded, eviction, or an external kill.
func CheckSIGKILL(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}

	var evidence []string
	forEachTermination(facts.Pod, func(name, _ string, t *corev1.ContainerStateTerminated) {
		if t.ExitCode != exitCodeSIGKILL || t.Reason == "OOMKilled" {
			return
		}
		evidence = append(evidence,
			fmt.Sprintf("container %q terminated: reason=%q exitCode=%d", name, t.Reason, t.ExitCode))
	})

	if len(evidence) == 0 {
		return nil
	}

	// Events that usually explain who sent the SIGKILL.
	for _, evt := range facts.Events {
		switch evt.Reason {
		case "Unhealthy", "Killing", "Evicted", "Preempting":
			evidence = append(evidence, fmt.Sprintf("event %q: %s", evt.Reason, evt.Message))
		}
	}

	return &Finding{
		Category: CategorySIGKILL,
		Detail:   "One or more containers were killed with SIGKILL (exit 137) but not by the OOM killer. Check for failing liveness probes, an exceeded terminationGracePeriodSeconds, or eviction.",
		Evidence: evidence,
		Severity: "warning",
	}
}

// forEachTermination calls fn for every current and last terminated state of
// every container and init container. name is the display name ("init:x" for
// init containers); key indexes the map returned by memoryLimits.
func forEachTermination(pod *corev1.Pod, fn func(name, key string, t *corev1.ContainerStateTerminated)) {
	visit := func(prefix string, statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			name := prefix + cs.Name
			for _, t := range []*corev1.ContainerStateTerminated{
				cs.State.Terminated,
				cs.LastTerminationState.Terminated, // captures a recently restarted container
			} {
				if t != nil {
					fn(name, name, t)
				}
			}
		}
	}
	visit("", pod.Status.ContainerStatuses)
	visit("init:", pod.Status.InitContainerStatuses)
}

// memoryLimits maps container display name → memory limit ("none" if unset).
func memoryLimits(pod *corev1.Pod) map[string]string {
	limits := make(map[string]string)
	add := func(prefix string, containers []corev1.Container) {
		for _, c := range containers {
			limits[prefix+c.Name] = "none"
			if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				limits[prefix+c.Name] = lim.String()
			}
		}
	}
	add("", pod.Spec.Containers)
	add("init:", pod.Spec.InitContainers)
	return limits
}
