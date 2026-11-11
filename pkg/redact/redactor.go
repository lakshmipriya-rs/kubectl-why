// SPDX-License-Identifier: MIT

// Package redact strips secrets and credential-like patterns from pod facts
// before they are sent to an LLM or written to output.
package redact

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

const placeholder = "[REDACTED]"

// credentialPatterns matches common credential / secret patterns in log lines.
var credentialPatterns = []*regexp.Regexp{
	// AWS access keys: AKIA…
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	// Generic secret/token env assignments: SECRET=value TOKEN=value PASSWORD=value
	regexp.MustCompile(`(?i)(password|passwd|secret|token|apikey|api_key|credential|auth)\s*[:=]\s*\S+`),
	// Bearer tokens
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`),
	// OpenAI / Anthropic style keys: sk-… or sk-ant-…
	regexp.MustCompile(`\bsk-[A-Za-z0-9\-]{20,}\b`),
	// Base64 blobs ≥ 32 chars (common for encoded secrets)
	regexp.MustCompile(`[A-Za-z0-9+/]{32,}={0,2}`),
	// Generic hex secrets ≥ 32 hex chars
	regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),
}

// RedactedFacts is a sanitised view of PodFacts safe to send over the network.
type RedactedFacts struct {
	PodName        string            `json:"pod_name"`
	Namespace      string            `json:"namespace"`
	Phase          string            `json:"phase"`
	ContainerStates []ContainerSnap  `json:"container_states"`
	Events         []EventSnap       `json:"events"`
	CurrentLogs    map[string]string `json:"current_logs"`
	PreviousLogs   map[string]string `json:"previous_logs"`
	Owner          OwnerSnap         `json:"owner"`
	RelatedCMs     []string          `json:"related_config_maps"`
	RelatedSecrets []string          `json:"related_secrets"` // names only
	PVCNames       []string          `json:"pvc_names"`
}

// ContainerSnap is a sanitised view of one container's state.
type ContainerSnap struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
	State        string `json:"state"` // "running", "waiting:<reason>", "terminated:<reason>:<exit>"
}

// EventSnap is a sanitised event.
type EventSnap struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Count   int32  `json:"count"`
}

// OwnerSnap is the sanitised owner chain.
type OwnerSnap struct {
	ReplicaSet string `json:"replica_set,omitempty"`
	Deployment string `json:"deployment,omitempty"`
}

// Redact converts PodFacts into RedactedFacts with secrets stripped.
func Redact(facts *collect.PodFacts) *RedactedFacts {
	if facts == nil || facts.Pod == nil {
		return &RedactedFacts{}
	}
	pod := facts.Pod

	r := &RedactedFacts{
		PodName:     pod.Name,
		Namespace:   pod.Namespace,
		Phase:       string(pod.Status.Phase),
		RelatedCMs:  facts.RelatedConfigMaps,
		RelatedSecrets: facts.RelatedSecrets, // names only, no values
		Owner: OwnerSnap{
			ReplicaSet: facts.Owner.ReplicaSetName,
			Deployment: facts.Owner.DeploymentName,
		},
	}

	// Container states.
	for _, cs := range pod.Status.ContainerStatuses {
		snap := ContainerSnap{
			Name:         cs.Name,
			Image:        cs.Image,
			Ready:        cs.Ready,
			RestartCount: cs.RestartCount,
		}
		switch {
		case cs.State.Running != nil:
			snap.State = "running"
		case cs.State.Waiting != nil:
			snap.State = "waiting:" + cs.State.Waiting.Reason
		case cs.State.Terminated != nil:
			snap.State = "terminated:" + cs.State.Terminated.Reason + ":" +
				strings.TrimRight(fmt.Sprint(cs.State.Terminated.ExitCode), " ")
		}
		r.ContainerStates = append(r.ContainerStates, snap)
	}

	// Events (sanitise message).
	for _, evt := range facts.Events {
		r.Events = append(r.Events, EventSnap{
			Reason:  evt.Reason,
			Message: redactString(evt.Message),
			Count:   evt.Count,
		})
	}

	// Logs (redacted).
	r.CurrentLogs = make(map[string]string, len(facts.CurrentLogs))
	for k, v := range facts.CurrentLogs {
		r.CurrentLogs[k] = redactString(v)
	}
	r.PreviousLogs = make(map[string]string, len(facts.PreviousLogs))
	for k, v := range facts.PreviousLogs {
		r.PreviousLogs[k] = redactString(v)
	}

	// PVC names.
	for _, pvc := range facts.PVCs {
		r.PVCNames = append(r.PVCNames, pvc.Name)
	}

	return r
}

// redactString replaces credential-like patterns with [REDACTED].
func redactString(s string) string {
	for _, re := range credentialPatterns {
		s = re.ReplaceAllString(s, placeholder)
	}
	return s
}
