// SPDX-License-Identifier: MIT

package rules

import (
	"strconv"

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
	src := podSource(facts.Pod)

	var evidence []Evidence
	limitShown := map[string]bool{}
	forEachTermination(facts.Pod, func(t termination) {
		if t.state.Reason != "OOMKilled" {
			return
		}
		evidence = append(evidence, t.evidence(src)...)
		// Show each container's memory limit once, even if it was OOM-killed
		// in both its current and previous state.
		key := specField(t.init, t.container)
		if !limitShown[key] {
			limitShown[key] = true
			evidence = append(evidence, Evidence{
				Source: src,
				Field:  key + ".resources.limits.memory",
				Value:  memoryLimit(facts.Pod, t.init, t.container),
			})
		}
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
	src := podSource(facts.Pod)

	var evidence []Evidence
	forEachTermination(facts.Pod, func(t termination) {
		if t.state.ExitCode != exitCodeSIGKILL || t.state.Reason == "OOMKilled" {
			return
		}
		evidence = append(evidence, t.evidence(src)...)
	})

	if len(evidence) == 0 {
		return nil
	}

	// Events that usually explain who sent the SIGKILL.
	for _, evt := range facts.Events {
		switch evt.Reason {
		case "Unhealthy", "Killing", "Evicted", "Preempting":
			evidence = append(evidence, eventEvidence(evt))
		}
	}

	return &Finding{
		Category: CategorySIGKILL,
		Detail:   "One or more containers were killed with SIGKILL (exit 137) but not by the OOM killer. Check for failing liveness probes, an exceeded terminationGracePeriodSeconds, or eviction.",
		Evidence: evidence,
		Severity: "warning",
	}
}

// termination is one terminated state (current or last) of one container.
type termination struct {
	init      bool
	container string
	last      bool // true for lastState (a previous run of a restarted container)
	state     *corev1.ContainerStateTerminated
}

// evidence describes the termination's reason and exit code.
func (t termination) evidence(src string) []Evidence {
	stateName := ".state"
	if t.last {
		stateName = ".lastState"
	}
	f := statusField(t.init, t.container) + stateName + ".terminated"
	return []Evidence{
		{Source: src, Field: f + ".reason", Value: t.state.Reason},
		{Source: src, Field: f + ".exitCode", Value: strconv.Itoa(int(t.state.ExitCode))},
	}
}

// forEachTermination calls fn for every current and last terminated state of
// every container and init container.
func forEachTermination(pod *corev1.Pod, fn func(termination)) {
	visit := func(init bool, statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			if cs.State.Terminated != nil {
				fn(termination{init: init, container: cs.Name, state: cs.State.Terminated})
			}
			if cs.LastTerminationState.Terminated != nil {
				fn(termination{init: init, container: cs.Name, last: true, state: cs.LastTerminationState.Terminated})
			}
		}
	}
	visit(false, pod.Status.ContainerStatuses)
	visit(true, pod.Status.InitContainerStatuses)
}

// memoryLimit returns the container's memory limit, or "none" if unset.
func memoryLimit(pod *corev1.Pod, init bool, name string) string {
	containers := pod.Spec.Containers
	if init {
		containers = pod.Spec.InitContainers
	}
	for _, c := range containers {
		if c.Name != name {
			continue
		}
		if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
			return lim.String()
		}
	}
	return "none"
}
