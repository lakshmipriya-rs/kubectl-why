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
)

// Reporter writes diagnostic output to a writer.
type Reporter struct {
	w io.Writer
}

// New returns a Reporter that writes to w.
func New(w io.Writer) *Reporter { return &Reporter{w: w} }

// Result is the full output shape (used for JSON mode).
type Result struct {
	Findings []rules.Finding `json:"findings"`
	LLM      *llm.Response   `json:"llm,omitempty"`
}

// Print formats findings and the LLM response according to format ("text"|"json").
func (r *Reporter) Print(findings []rules.Finding, resp *llm.Response, format string) error {
	result := Result{Findings: findings, LLM: resp}

	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(r.w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		return r.printText(findings, resp)
	}
}

func (r *Reporter) printText(findings []rules.Finding, resp *llm.Response) error {
	hr := strings.Repeat("─", 60)

	if len(findings) == 0 && (resp == nil || resp.RootCause == "") {
		fmt.Fprintln(r.w, "No problems detected by rules engine. Try --no-llm=false with a model for deeper analysis.")
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
					fmt.Fprintf(r.w, "     • %s\n", e)
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

		if len(resp.Evidence) > 0 {
			fmt.Fprintln(r.w, "Supported evidence:")
			for _, e := range resp.Evidence {
				fmt.Fprintf(r.w, "  • %s\n", e)
			}
			fmt.Fprintln(r.w)
		}

		if resp.Fix != "" {
			fmt.Fprintf(r.w, "Suggested fix:\n  %s\n", resp.Fix)
		}
	}

	fmt.Fprintln(r.w)
	return nil
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
