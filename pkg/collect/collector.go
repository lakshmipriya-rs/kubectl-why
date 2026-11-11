// SPDX-License-Identifier: MIT

package collect

import (
	"bytes"
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const maxLogLines = 100

// Collector gathers pod facts from the Kubernetes API. It is strictly read-only.
type Collector struct {
	client kubernetes.Interface
}

// New returns a new Collector backed by the given client.
func New(client kubernetes.Interface) *Collector {
	return &Collector{client: client}
}

// Collect fetches all diagnostic facts for podName in namespace.
func (c *Collector) Collect(ctx context.Context, podName, namespace string) (*PodFacts, error) {
	facts := &PodFacts{
		CurrentLogs:  make(map[string]string),
		PreviousLogs: make(map[string]string),
	}

	// ── Pod ──────────────────────────────────────────────────────────────────
	pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting pod %s/%s: %w", namespace, podName, err)
	}
	facts.Pod = pod

	// ── Events ───────────────────────────────────────────────────────────────
	evtList, err := c.client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", podName),
	})
	if err == nil {
		facts.Events = evtList.Items
	}

	// ── Logs (current + previous) ─────────────────────────────────────────────
	tailLines := int64(maxLogLines)
	for _, cs := range pod.Spec.Containers {
		logs, _ := c.fetchLogs(ctx, namespace, podName, cs.Name, false, tailLines)
		facts.CurrentLogs[cs.Name] = logs

		prevLogs, _ := c.fetchLogs(ctx, namespace, podName, cs.Name, true, tailLines)
		facts.PreviousLogs[cs.Name] = prevLogs
	}
	// Init containers too.
	for _, cs := range pod.Spec.InitContainers {
		logs, _ := c.fetchLogs(ctx, namespace, podName, cs.Name, false, tailLines)
		facts.CurrentLogs["init:"+cs.Name] = logs
	}

	// ── Owner chain ───────────────────────────────────────────────────────────
	facts.Owner = c.resolveOwnerChain(ctx, pod, namespace)

	// ── Related CM / Secret names (no contents) ───────────────────────────────
	facts.RelatedConfigMaps, facts.RelatedSecrets = extractRefNames(pod)

	// ── PVCs ─────────────────────────────────────────────────────────────────
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			pvc, err := c.client.CoreV1().PersistentVolumeClaims(namespace).Get(
				ctx, vol.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
			if err == nil {
				facts.PVCs = append(facts.PVCs, *pvc)
			}
		}
	}

	// ── Node conditions (Pending pods only) ───────────────────────────────────
	if pod.Status.Phase == corev1.PodPending && pod.Spec.NodeName != "" {
		node, err := c.client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
		if err == nil {
			facts.NodeConditions = node.Status.Conditions
		}
	}

	return facts, nil
}

// fetchLogs retrieves up to tailLines from the named container.
func (c *Collector) fetchLogs(ctx context.Context, namespace, pod, container string, previous bool, tailLines int64) (string, error) {
	opts := &corev1.PodLogOptions{
		Container: container,
		Previous:  previous,
		TailLines: &tailLines,
	}
	req := c.client.CoreV1().Pods(namespace).GetLogs(pod, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// resolveOwnerChain walks from Pod → ReplicaSet → Deployment.
func (c *Collector) resolveOwnerChain(ctx context.Context, pod *corev1.Pod, namespace string) OwnerChain {
	chain := OwnerChain{}
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			chain.ReplicaSetName = ref.Name
			rs, err := c.client.AppsV1().ReplicaSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
			if err != nil {
				break
			}
			chain.ReplicaSetLabels = rs.Labels
			for _, rsRef := range rs.OwnerReferences {
				if rsRef.Kind == "Deployment" {
					chain.DeploymentName = rsRef.Name
					dep, err := c.client.AppsV1().Deployments(namespace).Get(ctx, rsRef.Name, metav1.GetOptions{})
					if err == nil {
						chain.DeploymentLabels = dep.Labels
					}
					break
				}
			}
			break
		}
	}
	return chain
}

// extractRefNames returns CM and Secret names referenced by the pod (volumes + envFrom).
// It never fetches their contents.
func extractRefNames(pod *corev1.Pod) (cms, secrets []string) {
	seen := map[string]bool{}
	addCM := func(name string) {
		if name != "" && !seen["cm:"+name] {
			seen["cm:"+name] = true
			cms = append(cms, name)
		}
	}
	addSecret := func(name string) {
		if name != "" && !seen["sec:"+name] {
			seen["sec:"+name] = true
			secrets = append(secrets, name)
		}
	}

	for _, vol := range pod.Spec.Volumes {
		if vol.ConfigMap != nil {
			addCM(vol.ConfigMap.Name)
		}
		if vol.Secret != nil {
			addSecret(vol.Secret.SecretName)
		}
	}

	allContainers := append(pod.Spec.InitContainers, pod.Spec.Containers...)
	for _, c := range allContainers {
		for _, ef := range c.EnvFrom {
			if ef.ConfigMapRef != nil {
				addCM(ef.ConfigMapRef.Name)
			}
			if ef.SecretRef != nil {
				addSecret(ef.SecretRef.Name)
			}
		}
		for _, env := range c.Env {
			if env.ValueFrom != nil {
				if env.ValueFrom.ConfigMapKeyRef != nil {
					addCM(env.ValueFrom.ConfigMapKeyRef.Name)
				}
				if env.ValueFrom.SecretKeyRef != nil {
					addSecret(env.ValueFrom.SecretKeyRef.Name)
				}
			}
		}
	}
	return cms, secrets
}
