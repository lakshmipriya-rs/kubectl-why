// SPDX-License-Identifier: MIT

// Package report formats and prints diagnostic results.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/target"
)

// Reporter writes diagnostic output to a writer.
type Reporter struct {
	w io.Writer
}

// New returns a Reporter that writes to w.
func New(w io.Writer) *Reporter { return &Reporter{w: w} }

// Result is the full output shape (used for JSON mode).
type Result struct {
	Target   *target.Target  `json:"target,omitempty"`
	Findings []rules.Finding `json:"findings"`
	LLM      *llm.Response   `json:"llm,omitempty"`
}

// Print formats the result according to format ("text"|"json").
func (r *Reporter) Print(result Result, format string) error {
	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(r.w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		r.printTarget(result.Target)
		podHealthy := result.Target != nil && !result.Target.PodUnhealthy
		return r.printText(result.Findings, result.LLM, podHealthy)
	}
}

// printTarget says what is being diagnosed, and which pod was chosen for a workload.
func (r *Reporter) printTarget(t *target.Target) {
	if t == nil {
		return
	}
	if !t.IsWorkload() {
		fmt.Fprintf(r.w, "Diagnosing Pod/%s\n", t.Pod)
		return
	}
	fmt.Fprintf(r.w, "Diagnosing %s/%s → pod %s (%d of %d pods unhealthy)\n",
		t.Kind, t.Name, t.Pod, t.PodsUnhealthy, t.PodsTotal)
}

// podHealthy is true when the diagnosed pod shows no sign of failing.
func (r *Reporter) printText(findings []rules.Finding, resp *llm.Response, podHealthy bool) error {
	hr := strings.Repeat("─", 60)

	if len(findings) == 0 && (resp == nil || resp.RootCause == "") {
		if podHealthy {
			fmt.Fprintln(r.w, "✅ The pod looks healthy: running, ready, no restarts. No problems detected.")
			return nil
		}
		fmt.Fprintln(r.w, "No problems detected by the rule engine.")
		if resp == nil {
			fmt.Fprintln(r.w, "The pod shows signs of failing that no rule covers yet. Re-run with --explain for an LLM analysis.")
		}
		return nil
	}

	// ── Rule findings ─────────────────────────────────────────────────────────
	if len(findings) > 0 {
		fmt.Fprintln(r.w, "\n🔍 Rule Engine Findings")
		fmt.Fprintln(r.w, hr)
		for i, f := range findings {
			icon := severityIcon(f.Severity)
			fmt.Fprintf(r.w, "%s [%s] %s\n", icon, f.Category, f.Detail)
			if len(f.Evidence) > 0 {
				fmt.Fprintln(r.w, "   Evidence:")
				for _, e := range f.Evidence {
					fmt.Fprintf(r.w, "     • [%s] %s\n", e.ID, e)
				}
			}
			if i < len(findings)-1 {
				fmt.Fprintln(r.w)
			}
		}
	}

	// ── LLM analysis ──────────────────────────────────────────────────────────
	if resp != nil && resp.RootCause != "" {
		fmt.Fprintf(r.w, "\n\n🤖 LLM Analysis  (confidence: %s)\n", resp.Confidence)
		fmt.Fprintln(r.w, hr)
		fmt.Fprintf(r.w, "Root cause:  %s\n\n", resp.RootCause)

		if len(resp.CausalChain) > 0 {
			fmt.Fprintln(r.w, "Causal chain:")
			for i, step := range resp.CausalChain {
				fmt.Fprintf(r.w, "  %d. %s\n", i+1, step)
			}
			fmt.Fprintln(r.w)
		}

		if len(resp.EvidenceIDs) > 0 {
			byID := evidenceByID(findings)
			fmt.Fprintln(r.w, "Cited evidence:")
			for _, id := range resp.EvidenceIDs {
				fmt.Fprintf(r.w, "  • [%s] %s\n", id, byID[id])
			}
			fmt.Fprintln(r.w)
		} else {
			fmt.Fprintln(r.w, "Cited evidence: none (claims above are not backed by collected evidence)")
			fmt.Fprintln(r.w)
		}

		if resp.Fix != "" {
			fmt.Fprintf(r.w, "Suggested fix:\n  %s\n", resp.Fix)
		}
	}

	fmt.Fprintln(r.w)
	return nil
}

func evidenceByID(findings []rules.Finding) map[string]rules.Evidence {
	m := map[string]rules.Evidence{}
	for _, f := range findings {
		for _, e := range f.Evidence {
			m[e.ID] = e
		}
	}
	return m
}

func severityIcon(sev string) string {
	switch sev {
	case "critical":
		return "🔴"
	case "warning":
		return "🟡"
	default:
		return "ℹ️ "
	}
}
