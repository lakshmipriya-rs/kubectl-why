// SPDX-License-Identifier: MIT

package collect

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeploymentFacts is the raw data collected about a Deployment's rollout.
type DeploymentFacts struct {
	Deployment *appsv1.Deployment
	// ReplicaSets owned by the Deployment (current and previous revisions).
	ReplicaSets []appsv1.ReplicaSet
	// ReplicaSetEvents holds events per owned ReplicaSet that should have pods
	// (spec.replicas > 0), keyed by ReplicaSet name (e.g. FailedCreate).
	ReplicaSetEvents map[string][]corev1.Event
	// Pods selected by the Deployment.
	Pods []corev1.Pod
}

// CollectDeployment fetches a Deployment, its ReplicaSets and their events, and its pods.
func (c *Collector) CollectDeployment(ctx context.Context, name, namespace string) (*DeploymentFacts, error) {
	d, err := c.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting deployment %s/%s: %w", namespace, name, err)
	}
	df := &DeploymentFacts{Deployment: d, ReplicaSetEvents: map[string][]corev1.Event{}}

	sel, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("deployment %s/%s has an invalid selector: %w", namespace, name, err)
	}
	opts := metav1.ListOptions{LabelSelector: sel.String()}

	rsList, err := c.client.AppsV1().ReplicaSets(namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing replicasets of %s/%s: %w", namespace, name, err)
	}
	for _, rs := range rsList.Items {
		if !metav1.IsControlledBy(&rs, d) {
			continue // matches the labels but belongs to someone else
		}
		df.ReplicaSets = append(df.ReplicaSets, rs)
		if rs.Spec.Replicas == nil || *rs.Spec.Replicas > 0 {
			df.ReplicaSetEvents[rs.Name] = c.objectEvents(ctx, namespace, "ReplicaSet", rs.Name)
		}
	}

	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing pods of %s/%s: %w", namespace, name, err)
	}
	df.Pods = pods.Items
	return df, nil
}
