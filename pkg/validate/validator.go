// SPDX-License-Identifier: MIT

// Package validate checks an LLM response against the rule engine's evidence,
// so that claims without traceable evidence are flagged instead of trusted.
package validate

import (
	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

// FilterUnsupported keeps only the evidence IDs in resp that exist in findings
// (duplicates removed, order kept). If the response makes a root-cause claim
// but cites no valid evidence, its confidence is downgraded to "low".
//
// RootCause, CausalChain and Fix are not rewritten: the report shows them
// next to the evidence they cite, or flags them as uncited.
func FilterUnsupported(resp *llm.Response, findings []rules.Finding) *llm.Response {
	if resp == nil {
		return &llm.Response{}
	}

	known := map[string]bool{}
	for _, f := range findings {
		for _, e := range f.Evidence {
			known[e.ID] = true
		}
	}

	out := *resp
	out.EvidenceIDs = nil
	seen := map[string]bool{}
	for _, id := range resp.EvidenceIDs {
		if known[id] && !seen[id] {
			seen[id] = true
			out.EvidenceIDs = append(out.EvidenceIDs, id)
		}
	}

	if out.RootCause != "" && len(out.EvidenceIDs) == 0 {
		out.Confidence = "low"
	}
	return &out
}
