// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/redact"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/report"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/validate"
)

// flags holds all CLI options.
type flags struct {
	namespace string
	noLLM     bool
	dryRun    bool
	provider  string
	model     string
	output    string
	kubeconfig string
}

var f flags

// rootCmd is the top-level command: kubectl why <pod>
var rootCmd = &cobra.Command{
	Use:   "why <pod>",
	Short: "Diagnose a failing Kubernetes pod and print a root-cause report",
	Long: `kubectl-why collects pod spec/status, events, and logs for a single failing
pod, runs deterministic rule detectors, optionally consults an LLM, and prints
a structured root-cause report. It is strictly read-only and never modifies
the cluster.`,
	Args: cobra.ExactArgs(1),
	RunE: run,
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
	podName := args[0]
	ctx := context.Background()

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

	// ── Collect ───────────────────────────────────────────────────────────────
	collector := collect.New(client)
	facts, err := collector.Collect(ctx, podName, f.namespace)
	if err != nil {
		return fmt.Errorf("collecting pod facts: %w", err)
	}

	// ── Rules ─────────────────────────────────────────────────────────────────
	findings := rules.RunAll(facts)

	// ── Redact ────────────────────────────────────────────────────────────────
	redacted := redact.Redact(facts)

	// ── Dry-run: print payload and stop ───────────────────────────────────────
	if f.dryRun {
		payload := struct {
			RedactedFacts *redact.RedactedFacts `json:"redacted_facts"`
			Findings      []rules.Finding       `json:"findings"`
		}{redacted, findings}
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

	req := llm.BuildRequest(redacted, findings)
	resp, err := provider.Complete(ctx, req)
	if err != nil {
		// Gracefully degrade: show rules output even if LLM fails.
		fmt.Fprintf(os.Stderr, "warning: LLM call failed (%v) – showing rules-only output\n", err)
		resp = &llm.Response{}
	}

	// ── Validate ──────────────────────────────────────────────────────────────
	resp = validate.FilterUnsupported(resp, facts)

	// ── Report ────────────────────────────────────────────────────────────────
	r := report.New(os.Stdout)
	return r.Print(findings, resp, f.output)
}
