// SPDX-License-Identifier: MIT

// Package target turns a command-line argument such as "web-1", "pod/web-1"
// or "deploy/web" into the pod to diagnose. For a workload it lists the
// workload's pods and picks the unhealthiest one.
package target

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Kind is a canonical workload kind.
type Kind string

const (
	KindPod         Kind = "Pod"
	KindDeployment  Kind = "Deployment"
	KindStatefulSet Kind = "StatefulSet"
	KindDaemonSet   Kind = "DaemonSet"
	KindReplicaSet  Kind = "ReplicaSet"
	KindJob         Kind = "Job"
	KindService     Kind = "Service"
)

// aliases maps the names kubectl accepts (singular, plural, short) to a Kind.
var aliases = map[string]Kind{
	"pod": KindPod, "pods": KindPod, "po": KindPod,
	"deployment": KindDeployment, "deployments": KindDeployment, "deploy": KindDeployment,
	"statefulset": KindStatefulSet, "statefulsets": KindStatefulSet, "sts": KindStatefulSet,
	"daemonset": KindDaemonSet, "daemonsets": KindDaemonSet, "ds": KindDaemonSet,
	"replicaset": KindReplicaSet, "replicasets": KindReplicaSet, "rs": KindReplicaSet,
	"job": KindJob, "jobs": KindJob,
	"service": KindService, "services": KindService, "svc": KindService,
}

// Supported lists the accepted kinds for help and error messages.
const Supported = "pod, deploy, sts, ds, rs, job, svc"

// Target describes what is being diagnosed.
type Target struct {
	// Kind and Name are what the user asked for, e.g. Deployment/web.
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// Pod is the pod chosen for diagnosis. Equal to Name when Kind is Pod.
	// Empty for a Service that selects no pods.
	Pod string `json:"pod"`
	// PodsTotal and PodsUnhealthy are only set for workloads.
	PodsTotal     int `json:"pods_total,omitempty"`
	PodsUnhealthy int `json:"pods_unhealthy,omitempty"`
	// PodUnhealthy says whether the chosen pod itself looks unhealthy
	// (see IsUnhealthy). Set by the caller once the pod has been fetched.
	PodUnhealthy bool `json:"pod_unhealthy"`
}

// IsUnhealthy reports whether a pod shows any sign of failing: a failed or
// pending phase, a container waiting with an error reason, a non-zero exit,
// failing readiness, or restarts.
func IsUnhealthy(p corev1.Pod) bool { return unhealthiness(p) > 0 }

// IsWorkload reports whether the user asked for a workload rather than a pod.
func (t Target) IsWorkload() bool { return t.Kind != KindPod }

// Parse splits "kind/name" (or a bare pod name) into a Kind and a name.
// Group suffixes such as "deployment.apps/web" are accepted.
func Parse(arg string) (Kind, string, error) {
	kindPart, name, hasSlash := strings.Cut(arg, "/")
	if !hasSlash {
		if arg == "" {
			return "", "", fmt.Errorf("empty target")
		}
		return KindPod, arg, nil
	}
	if name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("invalid target %q: want <name> or <kind>/<name>", arg)
	}
	kindPart, _, _ = strings.Cut(strings.ToLower(kindPart), ".") // drop ".apps", ".batch"
	kind, ok := aliases[kindPart]
	if !ok {
		return "", "", fmt.Errorf("unsupported kind %q in %q (supported: %s)", kindPart, arg, Supported)
	}
	return kind, name, nil
}

// Resolve parses arg and, for workloads, selects the pod to diagnose.
func Resolve(ctx context.Context, client kubernetes.Interface, namespace, arg string) (Target, error) {
	kind, name, err := Parse(arg)
	if err != nil {
		return Target{}, err
	}
	t := Target{Kind: kind, Name: name}
	if kind == KindPod {
		t.Pod = name
		return t, nil
	}

	selector, err := workloadSelector(ctx, client, namespace, kind, name)
	if err != nil {
		return t, err
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return t, fmt.Errorf("%s/%s has an invalid selector: %w", kind, name, err)
	}
	if sel.Empty() {
		if kind == KindService {
			return t, nil // a Service without selector (manual endpoints): no pod to diagnose
		}
		return t, fmt.Errorf("%s/%s has an empty selector", kind, name)
	}

	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return t, fmt.Errorf("listing pods for %s/%s: %w", kind, name, err)
	}
	if len(pods.Items) == 0 {
		if kind == KindService {
			return t, nil // selecting no pods is itself the diagnosis (see service rules)
		}
		return t, fmt.Errorf("%s/%s has no pods (selector %q). Pods may be failing to be created, "+
			"e.g. by a ResourceQuota or an admission policy: check `kubectl describe %s/%s -n %s` "+
			"and the events of its controller", kind, name, sel.String(), strings.ToLower(string(kind)), name, namespace)
	}

	chosen, unhealthy := pickPod(pods.Items)
	t.Pod = chosen.Name
	t.PodsTotal = len(pods.Items)
	t.PodsUnhealthy = unhealthy
	return t, nil
}

// workloadSelector fetches the workload and returns its pod selector.
func workloadSelector(ctx context.Context, client kubernetes.Interface, ns string, kind Kind, name string) (*metav1.LabelSelector, error) {
	get := metav1.GetOptions{}
	var (
		sel *metav1.LabelSelector
		err error
	)
	switch kind {
	case KindDeployment:
		d, e := client.AppsV1().Deployments(ns).Get(ctx, name, get)
		if e == nil {
			sel = d.Spec.Selector
		}
		err = e
	case KindStatefulSet:
		s, e := client.AppsV1().StatefulSets(ns).Get(ctx, name, get)
		if e == nil {
			sel = s.Spec.Selector
		}
		err = e
	case KindDaemonSet:
		d, e := client.AppsV1().DaemonSets(ns).Get(ctx, name, get)
		if e == nil {
			sel = d.Spec.Selector
		}
		err = e
	case KindReplicaSet:
		r, e := client.AppsV1().ReplicaSets(ns).Get(ctx, name, get)
		if e == nil {
			sel = r.Spec.Selector
		}
		err = e
	case KindJob:
		j, e := client.BatchV1().Jobs(ns).Get(ctx, name, get)
		if e == nil {
			sel = j.Spec.Selector
		}
		err = e
	case KindService:
		s, e := client.CoreV1().Services(ns).Get(ctx, name, get)
		if e == nil {
			sel = &metav1.LabelSelector{MatchLabels: s.Spec.Selector}
		}
		err = e
	default:
		return nil, fmt.Errorf("unsupported kind %q", kind)
	}
	if err != nil {
		return nil, fmt.Errorf("getting %s/%s in namespace %s: %w", kind, name, ns, err)
	}
	if sel == nil {
		return nil, fmt.Errorf("%s/%s has no selector", kind, name)
	}
	return sel, nil
}

// pickPod returns the pod most worth diagnosing and how many pods are unhealthy.
// Pods are ranked by unhealthiness, then restart count, then newest first.
func pickPod(pods []corev1.Pod) (corev1.Pod, int) {
	unhealthy := 0
	ranked := make([]corev1.Pod, len(pods))
	copy(ranked, pods)
	for _, p := range ranked {
		if unhealthiness(p) > 0 {
			unhealthy++
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if ua, ub := unhealthiness(a), unhealthiness(b); ua != ub {
			return ua > ub
		}
		if ra, rb := restarts(a), restarts(b); ra != rb {
			return ra > rb
		}
		return a.CreationTimestamp.After(b.CreationTimestamp.Time)
	})
	return ranked[0], unhealthy
}

// unhealthiness scores how clearly a pod is failing (0 = looks healthy).
func unhealthiness(p corev1.Pod) int {
	switch p.Status.Phase {
	case corev1.PodFailed:
		return 4
	case corev1.PodSucceeded:
		return 0
	}

	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	score := 0
	if p.Status.Phase == corev1.PodPending {
		score = 3
	}
	for _, cs := range statuses {
		switch {
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "ContainerCreating" && cs.State.Waiting.Reason != "PodInitializing":
			score = max(score, 3) // CrashLoopBackOff, ImagePullBackOff, CreateContainerConfigError…
		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0:
			score = max(score, 3)
		case !cs.Ready && cs.State.Running != nil:
			score = max(score, 2) // running but failing readiness
		case cs.RestartCount > 0:
			score = max(score, 1)
		}
	}
	return score
}

func restarts(p corev1.Pod) int32 {
	var n int32
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}
