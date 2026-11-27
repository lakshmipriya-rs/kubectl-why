// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// ServiceRuleFunc is the signature of rules that diagnose a Service.
type ServiceRuleFunc func(sf *collect.ServiceFacts) *Finding

var serviceRegistry = []ServiceRuleFunc{
	CheckSelectorMismatch,
	CheckNoReadyEndpoints,
	CheckServicePortMismatch,
}

// RunServiceRules runs every Service rule. Evidence IDs are assigned E1…;
// callers that merge these with pod findings should call AssignEvidenceIDs
// again on the combined list.
func RunServiceRules(sf *collect.ServiceFacts) []Finding {
	var findings []Finding
	for _, rule := range serviceRegistry {
		if f := rule(sf); f != nil {
			findings = append(findings, *f)
		}
	}
	AssignEvidenceIDs(findings)
	return findings
}

// CheckSelectorMismatch reports a Service whose selector matches no pods, and
// points at the pods that come closest.
func CheckSelectorMismatch(sf *collect.ServiceFacts) *Finding {
	if sf == nil || sf.Service == nil || len(sf.Service.Spec.Selector) == 0 {
		return nil // no selector: endpoints are managed by hand
	}
	if len(selectedPods(sf)) > 0 {
		return nil
	}

	svc := sf.Service
	src := "Service/" + svc.Name
	evidence := []Evidence{
		{Source: src, Field: "spec.selector", Value: formatLabels(svc.Spec.Selector)},
		{Source: src, Field: "selected pods", Value: fmt.Sprintf("0 of %d pods in namespace %q", len(activePods(sf.Pods)), svc.Namespace)},
	}

	detail := fmt.Sprintf("Service %q selects pods with %s, but no pod in namespace %q has those labels, so it has no endpoints and traffic to it fails. ",
		svc.Name, formatLabels(svc.Spec.Selector), svc.Namespace)

	near := nearMisses(svc.Spec.Selector, sf.Pods, 3)
	for _, nm := range near {
		evidence = append(evidence, Evidence{
			Source: "Pod/" + nm.pod.Name,
			Field:  "metadata.labels (selector keys)",
			Value:  formatPodLabelsFor(svc.Spec.Selector, nm.pod.Labels),
		})
	}
	if len(near) > 0 {
		detail += fmt.Sprintf("Closest match: pod %q, which %s. Fix the Service selector or the pod template labels so they agree.",
			near[0].pod.Name, describeMismatch(svc.Spec.Selector, near[0].pod.Labels))
	} else {
		detail += "No pod has any of these labels: the pods may not exist yet, or live in another namespace."
	}

	return &Finding{Category: CategorySelectorMismatch, Detail: detail, Evidence: evidence, Severity: "critical"}
}

// CheckNoReadyEndpoints reports a Service that selects pods, none of which is Ready.
func CheckNoReadyEndpoints(sf *collect.ServiceFacts) *Finding {
	if sf == nil || sf.Service == nil || len(sf.Service.Spec.Selector) == 0 {
		return nil
	}
	selected := selectedPods(sf)
	if len(selected) == 0 {
		return nil // CheckSelectorMismatch covers this
	}
	for _, p := range selected {
		if podReady(p) {
			return nil
		}
	}

	svc := sf.Service
	evidence := []Evidence{{
		Source: "Service/" + svc.Name,
		Field:  "selected pods",
		Value:  fmt.Sprintf("%d selected, 0 ready", len(selected)),
	}}
	for i, p := range selected {
		if i == 5 {
			break // enough to show the pattern
		}
		evidence = append(evidence, Evidence{
			Source: "Pod/" + p.Name,
			Field:  "status.conditions[Ready]",
			Value:  fmt.Sprintf("False (phase %s)", p.Status.Phase),
		})
	}
	return &Finding{
		Category: CategoryNoReadyEndpoints,
		Detail: fmt.Sprintf("Service %q selects %d pod(s) but none is Ready, so it has no endpoints and traffic to it fails. "+
			"The findings for the selected pod explain why it isn't ready.", svc.Name, len(selected)),
		Evidence: evidence,
		Severity: "critical",
	}
}

// CheckServicePortMismatch reports Service ports whose targetPort isn't a port
// the selected pods declare. Pods that declare no ports at all are skipped,
// because declaring container ports is optional.
func CheckServicePortMismatch(sf *collect.ServiceFacts) *Finding {
	if sf == nil || sf.Service == nil {
		return nil
	}
	selected := selectedPods(sf)
	if len(selected) == 0 {
		return nil
	}
	pod := selected[0] // pods of one Service normally share a template
	declared := podPorts(pod)
	if len(declared) == 0 {
		return nil
	}

	svc := sf.Service
	var (
		evidence []Evidence
		problems []string
	)
	for i, sp := range svc.Spec.Ports {
		target := sp.TargetPort
		if target.Type == intstr.Int && target.IntVal == 0 && target.StrVal == "" {
			target = intstr.FromInt32(sp.Port) // unset targetPort defaults to port
		}
		if portDeclared(target, declared) {
			continue
		}
		ref := sp.Name
		if ref == "" {
			ref = fmt.Sprint(i)
		}
		evidence = append(evidence, Evidence{
			Source: "Service/" + svc.Name,
			Field:  fmt.Sprintf("spec.ports[%s].targetPort", ref),
			Value:  target.String(),
		})
		if target.Type == intstr.String {
			problems = append(problems, fmt.Sprintf("port %d targets the named port %q, which the pods don't define", sp.Port, target.StrVal))
		} else {
			problems = append(problems, fmt.Sprintf("port %d targets container port %d, which the pods don't declare", sp.Port, target.IntVal))
		}
	}
	if len(problems) == 0 {
		return nil
	}

	evidence = append(evidence, Evidence{
		Source: "Pod/" + pod.Name,
		Field:  "spec.containers[*].ports",
		Value:  formatDeclaredPorts(declared),
	})
	return &Finding{
		Category: CategoryServicePortMismatch,
		Detail: fmt.Sprintf("Service %q: %s (they declare %s). Connections will be refused; set targetPort to the port the application listens on.",
			svc.Name, strings.Join(problems, "; "), formatDeclaredPorts(declared)),
		Evidence: evidence,
		Severity: "critical",
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// activePods excludes pods that can never be endpoints (Succeeded/Failed).
func activePods(pods []corev1.Pod) []corev1.Pod {
	var out []corev1.Pod
	for _, p := range pods {
		if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			out = append(out, p)
		}
	}
	return out
}

func selectedPods(sf *collect.ServiceFacts) []corev1.Pod {
	var out []corev1.Pod
	for _, p := range activePods(sf.Pods) {
		if matchesSelector(sf.Service.Spec.Selector, p.Labels) {
			out = append(out, p)
		}
	}
	return out
}

func matchesSelector(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

type nearMiss struct {
	pod     corev1.Pod
	matched int
}

// nearMisses returns up to n active pods matching the most selector pairs (at least one).
func nearMisses(selector map[string]string, pods []corev1.Pod, n int) []nearMiss {
	var out []nearMiss
	for _, p := range activePods(pods) {
		m := 0
		for k, v := range selector {
			if p.Labels[k] == v {
				m++
			}
		}
		if m > 0 {
			out = append(out, nearMiss{pod: p, matched: m})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].matched != out[j].matched {
			return out[i].matched > out[j].matched
		}
		return out[i].pod.Name < out[j].pod.Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// describeMismatch says how a pod's labels differ from a selector.
func describeMismatch(selector, labels map[string]string) string {
	var diffs []string
	for _, k := range sortedKeys(selector) {
		got, ok := labels[k]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("has no %q label (selector wants %s=%s)", k, k, selector[k]))
		case got != selector[k]:
			diffs = append(diffs, fmt.Sprintf("has %s=%s (selector wants %s=%s)", k, got, k, selector[k]))
		}
	}
	return strings.Join(diffs, " and ")
}

// formatPodLabelsFor shows a pod's values for the selector's keys.
func formatPodLabelsFor(selector, labels map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(selector) {
		v, ok := labels[k]
		if !ok {
			v = "(missing)"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ", ")
}

func formatLabels(m map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type declaredPort struct {
	name string
	port int32
}

func podPorts(p corev1.Pod) []declaredPort {
	var out []declaredPort
	for _, c := range p.Spec.Containers {
		for _, cp := range c.Ports {
			out = append(out, declaredPort{name: cp.Name, port: cp.ContainerPort})
		}
	}
	return out
}

func portDeclared(target intstr.IntOrString, declared []declaredPort) bool {
	for _, d := range declared {
		if target.Type == intstr.String && d.name != "" && d.name == target.StrVal {
			return true
		}
		if target.Type == intstr.Int && d.port == target.IntVal {
			return true
		}
	}
	return false
}

func formatDeclaredPorts(declared []declaredPort) string {
	var parts []string
	for _, d := range declared {
		s := fmt.Sprint(d.port)
		if d.name != "" {
			s += " (" + d.name + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
