// SPDX-License-Identifier: MIT

package rules_test

import (
	"strings"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

// evidenceContains reports whether any evidence item, rendered with String(),
// contains sub.
func evidenceContains(items []rules.Evidence, sub string) bool {
	for _, e := range items {
		if strings.Contains(e.String(), sub) {
			return true
		}
	}
	return false
}
