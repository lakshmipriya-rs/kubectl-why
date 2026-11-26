// SPDX-License-Identifier: MIT

package rules

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// CheckImagePullBackOff detects when a container is stuck waiting because its
// image cannot be pulled (ImagePullBackOff or ErrImagePull).
//
// The finding is triggered only by a container's waiting reason. Events are
// used as supporting evidence and only when they are about pulling an image,
// because generic "BackOff"/"Failed" events also appear for crash loops.
func CheckImagePullBackOff(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}
	src := podSource(facts.Pod)

	var evidence []Evidence
	collectPullErrors := func(init bool, statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			if cs.State.Waiting == nil || !isImagePullReason(cs.State.Waiting.Reason) {
				continue
			}
			f := statusField(init, cs.Name)
			evidence = append(evidence,
				Evidence{Source: src, Field: f + ".state.waiting.reason", Value: cs.State.Waiting.Reason},
				Evidence{Source: src, Field: f + ".image", Value: cs.Image},
			)
			if cs.State.Waiting.Message != "" {
				evidence = append(evidence,
					Evidence{Source: src, Field: f + ".state.waiting.message", Value: cs.State.Waiting.Message})
			}
		}
	}
	collectPullErrors(false, facts.Pod.Status.ContainerStatuses)
	collectPullErrors(true, facts.Pod.Status.InitContainerStatuses)

	// No container is in a pull-error state: this rule does not apply,
	// regardless of what events say.
	if len(evidence) == 0 {
		return nil
	}

	// Corroborate with pull-related events only.
	for _, evt := range facts.Events {
		if isImagePullEvent(evt) {
			evidence = append(evidence, eventEvidence(evt))
		}
	}

	return &Finding{
		Category: CategoryImagePullBackOff,
		Detail:   "One or more containers cannot pull their image. Check that the image tag exists and that imagePullSecrets are configured correctly.",
		Evidence: evidence,
		Severity: "critical",
	}
}

func isImagePullReason(reason string) bool {
	return reason == "ImagePullBackOff" || reason == "ErrImagePull"
}

// isImagePullEvent reports whether an event is about pulling an image, e.g.
// `Failed: Failed to pull image "x"` or `BackOff: Back-off pulling image "x"`,
// as opposed to `BackOff: Back-off restarting failed container`.
func isImagePullEvent(evt corev1.Event) bool {
	if evt.Reason != "Failed" && evt.Reason != "BackOff" {
		return false
	}
	msg := strings.ToLower(evt.Message)
	return strings.Contains(msg, "pull") || strings.Contains(msg, "image")
}
