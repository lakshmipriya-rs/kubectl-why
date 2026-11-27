// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// `storageclass.storage.k8s.io "fast" not found`
var storageClassNotFoundRe = regexp.MustCompile(`storageclass\.storage\.k8s\.io "([^"]+)" not found`)

// CheckUnboundPVC detects PersistentVolumeClaims the pod needs that don't
// exist, aren't bound to a volume, or have lost their volume. The PVC's own
// events usually carry the real reason (e.g. an unknown StorageClass).
func CheckUnboundPVC(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}
	src := podSource(facts.Pod)
	scheduled := facts.Pod.Spec.NodeName != ""

	var (
		evidence []Evidence
		details  []string
	)

	for _, name := range facts.MissingPVCs {
		evidence = append(evidence, claimRefEvidence(facts.Pod, src, name)...)
		evidence = append(evidence, Evidence{
			Source: "PersistentVolumeClaim/" + name, Field: "exists", Value: "false",
		})
		details = append(details, fmt.Sprintf("PersistentVolumeClaim %q does not exist. Create it, or fix claimName in the pod spec.", name))
	}

	for _, pvc := range facts.PVCs {
		if pvc.Status.Phase == corev1.ClaimBound {
			continue
		}
		events := facts.PVCEvents[pvc.Name]
		// With volumeBindingMode WaitForFirstConsumer a claim stays Pending
		// until the pod is scheduled. That's expected, not the cause.
		if pvc.Status.Phase == corev1.ClaimPending && !scheduled && waitsForConsumerOnly(events) {
			continue
		}

		pvcSrc := "PersistentVolumeClaim/" + pvc.Name
		evidence = append(evidence, claimRefEvidence(facts.Pod, src, pvc.Name)...)
		evidence = append(evidence,
			Evidence{Source: pvcSrc, Field: "status.phase", Value: string(pvc.Status.Phase)},
			Evidence{Source: pvcSrc, Field: "spec.storageClassName", Value: storageClassName(pvc)},
		)
		if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			evidence = append(evidence, Evidence{Source: pvcSrc, Field: "spec.resources.requests.storage", Value: req.String()})
		}
		for _, evt := range events {
			value := evt.Message
			if evt.Count > 1 {
				value = fmt.Sprintf("%s (x%d)", evt.Message, evt.Count)
			}
			evidence = append(evidence, Evidence{Source: pvcSrc, Field: "event " + evt.Reason, Value: value})
		}
		details = append(details, pvcDetail(pvc, events))
	}

	if len(details) == 0 {
		return nil
	}
	return &Finding{
		Category: CategoryUnboundPVC,
		Detail:   strings.Join(details, " "),
		Evidence: evidence,
		Severity: "critical",
	}
}

// pvcDetail explains why a claim isn't bound, from its phase and events.
func pvcDetail(pvc corev1.PersistentVolumeClaim, events []corev1.Event) string {
	if pvc.Status.Phase == corev1.ClaimLost {
		return fmt.Sprintf("PersistentVolumeClaim %q is Lost: the PersistentVolume it was bound to no longer exists. "+
			"Its data may be gone; restore the volume or recreate the claim.", pvc.Name)
	}

	prefix := fmt.Sprintf("PersistentVolumeClaim %q is %s (not bound to a volume), so the pod can't start. ", pvc.Name, pvc.Status.Phase)
	var all strings.Builder
	var lastFailure string
	for _, e := range events {
		all.WriteString(e.Message + "\n")
		if e.Reason == "ProvisioningFailed" {
			lastFailure = e.Message
		}
	}
	msgs := all.String()

	switch {
	case storageClassNotFoundRe.MatchString(msgs):
		sc := storageClassNotFoundRe.FindStringSubmatch(msgs)[1]
		return prefix + fmt.Sprintf("StorageClass %q does not exist. Use an existing class (kubectl get storageclass) or create it.", sc)
	case strings.Contains(msgs, "no storage class is set"):
		return prefix + "No storageClassName is set, the cluster has no default StorageClass, and no existing PersistentVolume matches. " +
			"Set storageClassName, mark a StorageClass as default, or create a matching PersistentVolume."
	case lastFailure != "":
		return prefix + "Provisioning the volume failed: " + lastFailure
	case strings.Contains(msgs, "waiting for a volume to be created"):
		return prefix + "It is waiting for an external provisioner (CSI driver) that hasn't created the volume. Check that the provisioner is running and its logs."
	case waitsForConsumerOnly(events):
		return prefix + "Its StorageClass binds on first use and no volume has been provisioned for the pod's node yet. Check the provisioner."
	default:
		return prefix + "No PersistentVolume has been bound to it. Check that a matching PersistentVolume or a working StorageClass exists."
	}
}

// waitsForConsumerOnly reports whether the claim's only story is "waiting for
// the first consumer", with no failure events.
func waitsForConsumerOnly(events []corev1.Event) bool {
	waiting := false
	for _, e := range events {
		switch e.Reason {
		case "WaitForFirstConsumer":
			waiting = true
		case "ProvisioningFailed":
			return false
		}
	}
	return waiting
}

func storageClassName(pvc corev1.PersistentVolumeClaim) string {
	if pvc.Spec.StorageClassName == nil {
		return "(not set: cluster default)"
	}
	if *pvc.Spec.StorageClassName == "" {
		return `"" (no class: only pre-created PersistentVolumes)`
	}
	return *pvc.Spec.StorageClassName
}

// claimRefEvidence points at the pod volume(s) that use claim name.
func claimRefEvidence(pod *corev1.Pod, src, name string) []Evidence {
	var out []Evidence
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == name {
			out = append(out, Evidence{
				Source: src,
				Field:  fmt.Sprintf("spec.volumes[%s].persistentVolumeClaim.claimName", v.Name),
				Value:  name,
			})
		}
	}
	return out
}
