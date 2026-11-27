// SPDX-License-Identifier: MIT

package collect

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceFacts is the raw data collected about a Service.
type ServiceFacts struct {
	Service *corev1.Service
	// Pods holds every pod in the Service's namespace, not only the selected
	// ones, so rules can point at the pod that *almost* matches a selector.
	Pods []corev1.Pod
}

// CollectService fetches a Service and the pods in its namespace.
func (c *Collector) CollectService(ctx context.Context, name, namespace string) (*ServiceFacts, error) {
	svc, err := c.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting service %s/%s: %w", namespace, name, err)
	}
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods in %s: %w", namespace, err)
	}
	return &ServiceFacts{Service: svc, Pods: pods.Items}, nil
}
