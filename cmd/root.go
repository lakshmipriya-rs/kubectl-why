// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/redact"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/report"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/target"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/validate"
)

// flags holds all CLI options.
type flags struct {
	namespace  string
	noLLM      bool
	dryRun     bool
	provider   string
	model      string
	output     string
	kubeconfig string
}

var f flags

// rootCmd is the top-level command: kubectl why <pod>
var rootCmd = &cobra.Command{
	Use:   "why <pod> | <kind>/<name>",
	Short: "Diagnose a failing Kubernetes pod or workload and print a root-cause report",
	Long: `kubectl-why collects pod spec/status, events, and logs for a failing pod,
runs deterministic rule detectors, optionally consults an LLM, and prints a
structured root-cause report. It is strictly read-only and never modifies
the cluster.

The target is a pod name, or a workload as <kind>/<name>
(kinds: ` + target.Supported + `). For a workload, its unhealthiest pod is diagnosed.`,
	Example: `  kubectl why web-7d9f-abcde
  kubectl why deploy/web -n shop
  kubectl why sts/postgres --output json`,
	// Validate the argument count and the target's format before touching the
	// cluster, so typos fail fast without needing a reachable cluster.
	Args: cobra.MatchAll(cobra.ExactArgs(1), func(_ *cobra.Command, args []string) error {
		_, _, err := target.Parse(args[0])
		return err
	}),
	RunE: run,
	// Runtime errors (pod not found, no pods…) print only the error, not the
	// whole usage text that would bury it.
	SilenceUsage: true,
}

// Execute is the entry point called from main.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.Flags().StringVarP(&f.namespace, "namespace", "n", "default", "Kubernetes namespace")
	rootCmd.Flags().BoolVar(&f.noLLM, "no-llm", false, "Rules only – no network LLM calls")
	rootCmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print the redacted payload and exit without calling the LLM")
	rootCmd.Flags().StringVar(&f.provider, "provider", "ollama", "LLM provider: ollama | anthropic | openai")
	rootCmd.Flags().StringVar(&f.model, "model", "", "Model name override (provider-specific default used when empty)")
	rootCmd.Flags().StringVar(&f.output, "output", "text", "Output format: text | json")
	rootCmd.Flags().StringVar(&f.kubeconfig, "kubeconfig", "", "Path to kubeconfig (defaults to KUBECONFIG env or ~/.kube/config)")
}

func run(cmd *cobra.Command, args []string) error {
	// Cancel API and LLM calls cleanly on Ctrl-C / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Build Kubernetes client ───────────────────────────────────────────────
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.kubeconfig != "" {
		loadingRules.ExplicitPath = f.kubeconfig
	}
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	restCfg, err := kubeConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("building kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}

	// ── Resolve target (pod, or a workload's unhealthiest pod) ────────────────
	tgt, err := target.Resolve(ctx, client, f.namespace, args[0])
	if err != nil {
		return err
	}

	// ── Collect ───────────────────────────────────────────────────────────────
	collector := collect.New(client)
	facts, err := collector.Collect(ctx, tgt.Pod, f.namespace)
	if err != nil {
		return fmt.Errorf("collecting pod facts: %w", err)
	}

	// ── Rules ─────────────────────────────────────────────────────────────────
	findings := rules.RunAll(facts)

	// ── Redact ────────────────────────────────────────────────────────────────
	// Everything that can leave the machine (dry-run payload, LLM prompt) uses
	// the redacted copies. The local report below still shows raw findings.
	redacted := redact.Redact(facts)
	redactedFindings := redact.Findings(findings)

	// ── Dry-run: print payload and stop ───────────────────────────────────────
	if f.dryRun {
		payload := struct {
			Target        target.Target         `json:"target"`
			RedactedFacts *redact.RedactedFacts `json:"redacted_facts"`
			Findings      []rules.Finding       `json:"findings"`
		}{tgt, redacted, redactedFindings}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	// ── LLM ───────────────────────────────────────────────────────────────────
	var provider llm.Provider
	if f.noLLM {
		provider = llm.NewNoop()
	} else {
		provider, err = llm.NewProvider(f.provider, f.model)
		if err != nil {
			return fmt.Errorf("creating LLM provider: %w", err)
		}
	}

	req := llm.BuildRequest(redacted, redactedFindings)
	resp, err := provider.Complete(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err() // interrupted by the user, not an LLM failure
		}
		// Gracefully degrade: show rules output even if LLM fails.
		fmt.Fprintf(os.Stderr, "warning: LLM call failed (%v) – showing rules-only output\n", err)
		resp = &llm.Response{}
	}

	// ── Validate ──────────────────────────────────────────────────────────────
	resp = validate.FilterUnsupported(resp, findings)

	// ── Report ────────────────────────────────────────────────────────────────
	r := report.New(os.Stdout)
	return r.Print(report.Result{Target: &tgt, Findings: findings, LLM: resp}, f.output)
}
