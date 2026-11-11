// SPDX-License-Identifier: MIT

package rules

import "github.com/lakshmipriya-rs/kubectl-why/pkg/collect"

// RuleFunc is the signature every rule must implement.
// It returns nil when the rule does not apply.
type RuleFunc func(facts *collect.PodFacts) *Finding

// registry holds all registered rules in priority order.
var registry = []RuleFunc{
	CheckImagePullBackOff,
	CheckOOMKilled,
	CheckFailedScheduling,
}

// RunAll executes every registered rule against facts and returns all findings.
// Rules are independent; multiple findings may be returned (e.g. two problems at once).
func RunAll(facts *collect.PodFacts) []Finding {
	var findings []Finding
	for _, rule := range registry {
		if f := rule(facts); f != nil {
			findings = append(findings, *f)
		}
	}
	return findings
}
