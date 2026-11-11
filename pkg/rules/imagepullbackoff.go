// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// CheckImagePullBackOff detects when a container is stuck waiting because its
// image cannot be pulled (ImagePullBackOff or ErrImagePull).
func CheckImagePullBackOff(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}

	var evidence []string
	for _, cs := range facts.Pod.Status.ContainerStatuses {
		if cs.State.Waiting == nil {
			continue
		}
		reason := cs.State.Waiting.Reason
		if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
			evidence = append(evidence,
				fmt.Sprintf("container %q: waiting reason=%q message=%q",
					cs.Name, reason, cs.State.Waiting.Message),
				fmt.Sprintf("image: %q", cs.Image),
			)
		}
	}
	// Also check init containers.
	for _, cs := range facts.Pod.Status.InitContainerStatuses {
		if cs.State.Waiting == nil {
			continue
		}
		reason := cs.State.Waiting.Reason
		if reason == "ImagePullBackOff" || reason == "ErrImagePull" {
			evidence = append(evidence,
				fmt.Sprintf("init container %q: waiting reason=%q message=%q",
					cs.Name, reason, cs.State.Waiting.Message),
				fmt.Sprintf("image: %q", cs.Image),
			)
		}
	}

	// Corroborate with events.
	for _, evt := range facts.Events {
		if evt.Reason == "Failed" || evt.Reason == "BackOff" {
			evidence = append(evidence, fmt.Sprintf("event %q: %s", evt.Reason, evt.Message))
		}
	}

	if len(evidence) == 0 {
		return nil
	}

	return &Finding{
		Category: CategoryImagePullBackOff,
		Detail:   "One or more containers cannot pull their image. Check that the image tag exists and that imagePullSecrets are configured correctly.",
		Evidence: evidence,
		Severity: "critical",
	}
}
