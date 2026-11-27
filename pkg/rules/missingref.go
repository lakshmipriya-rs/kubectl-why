// SPDX-License-Identifier: MIT

package rules

import (
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
)

// These rules rely only on the kubelet's error messages. kubectl-why never
// fetches ConfigMaps or Secrets, so it needs no RBAC on them and can never
// expose Secret contents.
var (
	// `configmap "app-config" not found`, `secret "db-creds" not found`
	// (env errors, and FailedMount: `MountVolume.SetUp failed for volume "x" : configmap "y" not found`)
	notFoundRe = regexp.MustCompile(`(?i)\b(configmap|secret)s? "([^"]+)" not found`)
	// `couldn't find key DB_HOST in ConfigMap default/app-config`
	missingKeyRe = regexp.MustCompile(`couldn't find key (\S+) in (ConfigMap|Secret) ([^/\s]+)/(\S+)`)
)

// missingRef is one ConfigMap/Secret (or one key in it) the pod needs but can't get.
type missingRef struct {
	kind string // "ConfigMap" or "Secret"
	name string
	key  string // empty: the whole object is missing
}

// CheckMissingConfigMap reports ConfigMaps (or keys in them) that the pod
// references but that don't exist.
func CheckMissingConfigMap(facts *collect.PodFacts) *Finding {
	return checkMissingRef(facts, "ConfigMap", CategoryMissingConfigMap)
}

// CheckMissingSecret reports Secrets (or keys in them) that the pod references
// but that don't exist.
func CheckMissingSecret(facts *collect.PodFacts) *Finding {
	return checkMissingRef(facts, "Secret", CategoryMissingSecret)
}

func checkMissingRef(facts *collect.PodFacts, kind, category string) *Finding {
	if facts.Pod == nil {
		return nil
	}
	src := podSource(facts.Pod)

	var (
		evidence []Evidence
		refs     []missingRef
		seen     = map[missingRef]bool{}
	)
	addRefs := func(msg string) bool {
		found := false
		for _, r := range parseMissingRefs(msg) {
			if r.kind != kind {
				continue
			}
			found = true
			if !seen[r] {
				seen[r] = true
				refs = append(refs, r)
			}
		}
		return found
	}

	// Container status: CreateContainerConfigError carries the exact message.
	check := func(init bool, statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			w := cs.State.Waiting
			if w == nil || w.Reason != "CreateContainerConfigError" || !addRefs(w.Message) {
				continue
			}
			f := statusField(init, cs.Name)
			evidence = append(evidence,
				Evidence{Source: src, Field: f + ".state.waiting.reason", Value: w.Reason},
				Evidence{Source: src, Field: f + ".state.waiting.message", Value: w.Message},
			)
		}
	}
	check(true, facts.Pod.Status.InitContainerStatuses)
	check(false, facts.Pod.Status.ContainerStatuses)

	// Events: FailedMount for volumes, Failed for env errors.
	for _, evt := range facts.Events {
		if (evt.Reason == "FailedMount" || evt.Reason == "Failed") && addRefs(evt.Message) {
			evidence = append(evidence, eventEvidence(evt))
		}
	}

	if len(refs) == 0 {
		return nil
	}

	// Show where the pod spec references each missing object, so the fix is obvious.
	for _, r := range refs {
		evidence = append(evidence, specRefs(facts.Pod, r, src)...)
	}

	return &Finding{
		Category: category,
		Detail:   missingRefDetail(refs, facts.Pod.Namespace),
		Evidence: evidence,
		Severity: "critical",
	}
}

// parseMissingRefs extracts every missing ConfigMap/Secret (or key) from a message.
func parseMissingRefs(msg string) []missingRef {
	var refs []missingRef
	for _, m := range notFoundRe.FindAllStringSubmatch(msg, -1) {
		kind := "ConfigMap"
		if strings.EqualFold(m[1], "secret") {
			kind = "Secret"
		}
		refs = append(refs, missingRef{kind: kind, name: m[2]})
	}
	for _, m := range missingKeyRe.FindAllStringSubmatch(msg, -1) {
		refs = append(refs, missingRef{kind: m[2], name: m[4], key: m[1]})
	}
	return refs
}

func missingRefDetail(refs []missingRef, namespace string) string {
	var parts []string
	kind := refs[0].kind
	for _, r := range refs {
		if r.key != "" {
			parts = append(parts, fmt.Sprintf("%s %q exists but has no key %q.", r.kind, r.name, r.key))
		} else {
			parts = append(parts, fmt.Sprintf("%s %q does not exist in namespace %q.", r.kind, r.name, namespace))
		}
	}
	fix := fmt.Sprintf(" The pod cannot start until it is fixed: create the %s (or add the key), correct the name in the pod spec, "+
		"or mark the reference optional: true if the application can run without it.", kind)
	return "The pod references " + kindPlural(kind, len(refs)) + " that it cannot load. " + strings.Join(parts, " ") + fix
}

func kindPlural(kind string, n int) string {
	if n == 1 {
		return "a " + kind
	}
	return kind + "s"
}

// specRefs finds every place in the pod spec that references r.
func specRefs(pod *corev1.Pod, r missingRef, src string) []Evidence {
	var out []Evidence
	add := func(field, value string) {
		out = append(out, Evidence{Source: src, Field: field, Value: value})
	}
	isCM := r.kind == "ConfigMap"

	// Volumes (matched by name: a volume mounts the whole object).
	for _, v := range pod.Spec.Volumes {
		base := fmt.Sprintf("spec.volumes[%s]", v.Name)
		switch {
		case isCM && v.ConfigMap != nil && v.ConfigMap.Name == r.name:
			add(base+".configMap.name", r.name)
		case !isCM && v.Secret != nil && v.Secret.SecretName == r.name:
			add(base+".secret.secretName", r.name)
		case v.Projected != nil:
			for i, s := range v.Projected.Sources {
				if isCM && s.ConfigMap != nil && s.ConfigMap.Name == r.name {
					add(fmt.Sprintf("%s.projected.sources[%d].configMap.name", base, i), r.name)
				}
				if !isCM && s.Secret != nil && s.Secret.Name == r.name {
					add(fmt.Sprintf("%s.projected.sources[%d].secret.name", base, i), r.name)
				}
			}
		}
	}

	// Container env and envFrom.
	scan := func(init bool, containers []corev1.Container) {
		for _, c := range containers {
			base := specField(init, c.Name)
			for i, ef := range c.EnvFrom {
				if isCM && ef.ConfigMapRef != nil && ef.ConfigMapRef.Name == r.name && r.key == "" {
					add(fmt.Sprintf("%s.envFrom[%d].configMapRef.name", base, i), r.name)
				}
				if !isCM && ef.SecretRef != nil && ef.SecretRef.Name == r.name && r.key == "" {
					add(fmt.Sprintf("%s.envFrom[%d].secretRef.name", base, i), r.name)
				}
			}
			for _, e := range c.Env {
				if e.ValueFrom == nil {
					continue
				}
				field := fmt.Sprintf("%s.env[%s].valueFrom", base, e.Name)
				if k := e.ValueFrom.ConfigMapKeyRef; isCM && k != nil && k.Name == r.name && (r.key == "" || k.Key == r.key) {
					add(field+".configMapKeyRef", k.Name+"/"+k.Key)
				}
				if k := e.ValueFrom.SecretKeyRef; !isCM && k != nil && k.Name == r.name && (r.key == "" || k.Key == r.key) {
					add(field+".secretKeyRef", k.Name+"/"+k.Key)
				}
			}
		}
	}
	scan(true, pod.Spec.InitContainers)
	scan(false, pod.Spec.Containers)
	return out
}
