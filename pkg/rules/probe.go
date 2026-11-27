// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// probeEventPrefixes maps the start of an "Unhealthy" event message to the probe type.
var probeEventPrefixes = []struct{ prefix, probe string }{
	{"Liveness probe failed", "liveness"},
	{"Readiness probe failed", "readiness"},
	{"Startup probe failed", "startup"},
}

// containerFieldPath extracts the container name from an event's
// involvedObject.fieldPath, e.g. "spec.containers{app}".
var containerFieldPath = regexp.MustCompile(`^spec\.(initContainers|containers)\{([^}]+)\}$`)

// probeFailure is one (container, probe type) pair with failing probes.
type probeFailure struct {
	container string // empty if the event doesn't say
	init      bool
	probe     string // "liveness" | "readiness" | "startup"
	events    []corev1.Event
}

// CheckProbeFailure detects failing liveness, readiness and startup probes
// from the kubelet's "Unhealthy" events, and shows the probe configuration
// next to the failure so a wrong port, path or timeout is easy to spot.
func CheckProbeFailure(facts *collect.PodFacts) *Finding {
	if facts.Pod == nil {
		return nil
	}

	// Group Unhealthy events by container and probe type, keeping event order.
	var failures []*probeFailure
	index := map[string]*probeFailure{}
	for _, evt := range facts.Events {
		if evt.Reason != "Unhealthy" {
			continue
		}
		probe := probeType(evt.Message)
		if probe == "" {
			continue
		}
		var container string
		var init bool
		if m := containerFieldPath.FindStringSubmatch(evt.InvolvedObject.FieldPath); m != nil {
			init, container = m[1] == "initContainers", m[2]
		}
		key := container + "/" + probe
		pf := index[key]
		if pf == nil {
			pf = &probeFailure{container: container, init: init, probe: probe}
			index[key] = pf
			failures = append(failures, pf)
		}
		pf.events = append(pf.events, evt)
	}
	if len(failures) == 0 {
		return nil
	}

	src := podSource(facts.Pod)
	var (
		evidence []Evidence
		hints    []string
		severity = "warning" // readiness/startup only: no restarts
	)
	for _, pf := range failures {
		if pf.probe == "liveness" {
			severity = "critical" // the kubelet restarts the container
		}
		for _, evt := range pf.events {
			evidence = append(evidence, eventEvidence(evt))
		}

		c := findContainer(facts.Pod, pf.init, pf.container)
		if c == nil {
			hints = append(hints, messageHint(pf))
			continue
		}
		field := specField(pf.init, c.Name) + "." + pf.probe + "Probe"
		if p := probeSpec(c, pf.probe); p != nil {
			evidence = append(evidence, Evidence{Source: src, Field: field, Value: describeProbe(p)})
			if hint, ev := portHint(c, p, pf, src); hint != "" {
				evidence = append(evidence, ev...)
				hints = append(hints, hint)
				continue // a wrong port explains the failure better than the message
			}
		}
		if ready, ok := containerReady(facts.Pod, pf.init, c.Name); ok {
			evidence = append(evidence, Evidence{
				Source: src,
				Field:  statusField(pf.init, c.Name) + ".ready",
				Value:  fmt.Sprintf("%t", ready),
			})
		}
		hints = append(hints, messageHint(pf))
	}

	return &Finding{
		Category: CategoryProbeFailure,
		Detail:   strings.Join(dedupe(hints), " "),
		Evidence: evidence,
		Severity: severity,
	}
}

func probeType(message string) string {
	for _, p := range probeEventPrefixes {
		if strings.HasPrefix(message, p.prefix) {
			return p.probe
		}
	}
	return ""
}

// messageHint explains a probe failure from the kubelet's message.
func messageHint(pf *probeFailure) string {
	who := "The " + pf.probe + " probe"
	if pf.container != "" {
		who = fmt.Sprintf("The %s probe of container %q", pf.probe, pf.container)
	}
	var effect string
	switch pf.probe {
	case "liveness":
		effect = " Each time it fails enough times in a row, the container is restarted."
	case "readiness":
		effect = " While it fails, the pod receives no traffic from Services (no restart)."
	case "startup":
		effect = " Until it succeeds, liveness and readiness probes don't run; if it never succeeds, the container is restarted."
	}

	msg := strings.ToLower(pf.events[len(pf.events)-1].Message)
	var cause string
	switch {
	case strings.Contains(msg, "connection refused"):
		cause = " fails with \"connection refused\": nothing is listening on the probed port. Check the port, or give a slow-starting app more time (initialDelaySeconds or a startupProbe)."
	case strings.Contains(msg, "statuscode: 404"):
		cause = " gets HTTP 404: the health-check path does not exist. Check the probe's path."
	case strings.Contains(msg, "statuscode: 401"), strings.Contains(msg, "statuscode: 403"):
		cause = " gets HTTP 401/403: the health-check endpoint requires authentication. Use an unauthenticated health path."
	case strings.Contains(msg, "statuscode: 5"):
		cause = " gets an HTTP 5xx error: the application reports itself unhealthy. Check its logs."
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		cause = " times out: the application responds too slowly. Check its load, or raise timeoutSeconds."
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "no route to host"):
		cause = " cannot reach the probed host. A probe normally targets the pod itself; remove the probe's host setting."
	default:
		cause = " is failing."
	}
	return who + cause + effect
}

// portHint checks that an httpGet/tcpSocket/grpc probe targets a port the
// container declares. It only reports a problem when the container declares
// ports at all (declaring ports is optional in Kubernetes).
func portHint(c *corev1.Container, p *corev1.Probe, pf *probeFailure, src string) (string, []Evidence) {
	var port *intstr.IntOrString
	switch {
	case p.HTTPGet != nil:
		port = &p.HTTPGet.Port
	case p.TCPSocket != nil:
		port = &p.TCPSocket.Port
	case p.GRPC != nil:
		port = &intstr.IntOrString{Type: intstr.Int, IntVal: p.GRPC.Port}
	default:
		return "", nil
	}
	if len(c.Ports) == 0 {
		return "", nil
	}

	var declared []string
	for _, cp := range c.Ports {
		if port.Type == intstr.String && cp.Name == port.StrVal {
			return "", nil
		}
		if port.Type == intstr.Int && cp.ContainerPort == port.IntVal {
			return "", nil
		}
		declared = append(declared, fmt.Sprintf("%d", cp.ContainerPort))
		if cp.Name != "" {
			declared[len(declared)-1] += " (" + cp.Name + ")"
		}
	}

	ev := []Evidence{{
		Source: src,
		Field:  specField(pf.init, c.Name) + ".ports",
		Value:  strings.Join(declared, ", "),
	}}
	if port.Type == intstr.String {
		return fmt.Sprintf("The %s probe of container %q uses the named port %q, which the container does not define (it defines %s).",
			pf.probe, c.Name, port.StrVal, strings.Join(declared, ", ")), ev
	}
	return fmt.Sprintf("The %s probe of container %q targets port %d, but the container's ports are %s. The probe is probably pointing at the wrong port.",
		pf.probe, c.Name, port.IntVal, strings.Join(declared, ", ")), ev
}

// describeProbe renders a probe's action and timing on one line, e.g.
// "httpGet :8080/healthz, initialDelaySeconds=0 periodSeconds=10 timeoutSeconds=1 failureThreshold=3".
func describeProbe(p *corev1.Probe) string {
	var action string
	switch {
	case p.HTTPGet != nil:
		action = fmt.Sprintf("httpGet :%s%s", p.HTTPGet.Port.String(), p.HTTPGet.Path)
		if p.HTTPGet.Host != "" {
			action = fmt.Sprintf("httpGet %s:%s%s", p.HTTPGet.Host, p.HTTPGet.Port.String(), p.HTTPGet.Path)
		}
	case p.TCPSocket != nil:
		action = "tcpSocket :" + p.TCPSocket.Port.String()
	case p.GRPC != nil:
		action = fmt.Sprintf("grpc :%d", p.GRPC.Port)
	case p.Exec != nil:
		action = fmt.Sprintf("exec %q", p.Exec.Command)
	default:
		action = "unknown action"
	}
	return fmt.Sprintf("%s, initialDelaySeconds=%d periodSeconds=%d timeoutSeconds=%d failureThreshold=%d",
		action, p.InitialDelaySeconds, p.PeriodSeconds, p.TimeoutSeconds, p.FailureThreshold)
}

func probeSpec(c *corev1.Container, probe string) *corev1.Probe {
	switch probe {
	case "liveness":
		return c.LivenessProbe
	case "readiness":
		return c.ReadinessProbe
	case "startup":
		return c.StartupProbe
	}
	return nil
}

func findContainer(pod *corev1.Pod, init bool, name string) *corev1.Container {
	if name == "" {
		return nil
	}
	containers := pod.Spec.Containers
	if init {
		containers = pod.Spec.InitContainers
	}
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func containerReady(pod *corev1.Pod, init bool, name string) (bool, bool) {
	statuses := pod.Status.ContainerStatuses
	if init {
		statuses = pod.Status.InitContainerStatuses
	}
	for _, cs := range statuses {
		if cs.Name == name {
			return cs.Ready, true
		}
	}
	return false, false
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
