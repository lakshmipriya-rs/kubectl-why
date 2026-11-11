// SPDX-License-Identifier: MIT

package collect

import (
	corev1 "k8s.io/api/core/v1"
)

// OwnerChain holds the ownership ancestry of a pod (e.g. Pod → ReplicaSet → Deployment).
type OwnerChain struct {
	ReplicaSetName string
	DeploymentName string
	// Raw owner references at each level for future revision-diff support.
	ReplicaSetLabels  map[string]string
	DeploymentLabels  map[string]string
}

// PodFacts is the raw data collected about a failing pod.
// It never contains Secret or ConfigMap contents – only their names.
type PodFacts struct {
	Pod          *corev1.Pod
	Events       []corev1.Event
	CurrentLogs  map[string]string // container name -> last ~100 lines
	PreviousLogs map[string]string // container name -> last ~100 lines (--previous)

	Owner OwnerChain

	// Names only – contents are NEVER collected.
	RelatedConfigMaps []string
	RelatedSecrets    []string

	PVCs []corev1.PersistentVolumeClaim

	// Populated when pod phase is Pending.
	NodeConditions []corev1.NodeCondition
}
