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
		return r.printText(result.Findings, result.LLM, result.Target)
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
	if t.Pod == "" {
		what := "no pods"
		if t.Kind == target.KindService {
			what = "no pods selected"
		}
		fmt.Fprintf(r.w, "Diagnosing %s/%s (%s)\n", t.Kind, t.Name, what)
		return
	}
	fmt.Fprintf(r.w, "Diagnosing %s/%s → pod %s (%d of %d pods unhealthy)\n",
		t.Kind, t.Name, t.Pod, t.PodsUnhealthy, t.PodsTotal)
}

// healthyMessage returns the all-clear message for t, if it looks healthy.
func healthyMessage(t *target.Target) (string, bool) {
	switch {
	case t == nil:
		return "", false
	case t.Kind == target.KindService && t.Pod == "":
		// No findings and no pods: the Service has no selector.
		return fmt.Sprintf("Service %q has no selector: its endpoints are managed manually, so there are no pods to check.", t.Name), true
	case t.Pod == "":
		// A Deployment with no pods and no finding explaining it.
		return fmt.Sprintf("%s %q has no pods and no rule found a reason. It may be scaled to 0 replicas (kubectl get %s %s).",
			t.Kind, t.Name, strings.ToLower(string(t.Kind)), t.Name), true
	case t.PodUnhealthy:
		return "", false
	case t.Kind == target.KindService:
		return fmt.Sprintf("✅ Service %q selects %d pod(s) and has ready endpoints. No problems detected.", t.Name, t.PodsTotal), true
	default:
		return "✅ The pod looks healthy: running, ready, no restarts. No problems detected.", true
	}
}

func (r *Reporter) printText(findings []rules.Finding, resp *llm.Response, t *target.Target) error {
	hr := strings.Repeat("─", 60)

	if len(findings) == 0 && (resp == nil || resp.RootCause == "") {
		if msg, ok := healthyMessage(t); ok {
			fmt.Fprintln(r.w, msg)
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
