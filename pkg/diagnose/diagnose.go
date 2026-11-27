// SPDX-License-Identifier: MIT

// Package diagnose runs a complete rules-based diagnosis of one target
// (a pod, workload or Service). It is shared by the CLI and the MCP server.
package diagnose

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/redact"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/target"
)

// Result is the outcome of a diagnosis.
type Result struct {
	// Target says what was diagnosed (and which pod was chosen for a workload).
	Target target.Target
	// Findings from all rules, with evidence IDs E1…En across all of them.
	// Evidence is NOT redacted; use Redacted before it leaves the machine.
	Findings []rules.Finding
	// Facts about the diagnosed pod; nil when there is no pod
	// (e.g. a Service that selects nothing).
	Facts *collect.PodFacts
}

// Run diagnoses arg ("web-1", "deploy/web", "svc/web", …) in namespace.
// It only reads from the cluster.
func Run(ctx context.Context, client kubernetes.Interface, namespace, arg string) (*Result, error) {
	tgt, err := target.Resolve(ctx, client, namespace, arg)
	if err != nil {
		return nil, err
	}

	collector := collect.New(client)
	res := &Result{}

	// A Service gets its own checks (selector, endpoints, ports) first.
	if tgt.Kind == target.KindService {
		sf, err := collector.CollectService(ctx, tgt.Name, namespace)
		if err != nil {
			return nil, fmt.Errorf("collecting service facts: %w", err)
		}
		res.Findings = append(res.Findings, rules.RunServiceRules(sf)...)
	}
	// A Deployment gets rollout checks (pod creation failures, stuck rollouts).
	if tgt.Kind == target.KindDeployment {
		df, err := collector.CollectDeployment(ctx, tgt.Name, namespace)
		if err != nil {
			return nil, fmt.Errorf("collecting deployment facts: %w", err)
		}
		res.Findings = append(res.Findings, rules.RunDeploymentRules(df)...)
	}

	// Then the pod (for a workload or Service: its unhealthiest pod, if any).
	if tgt.Pod != "" {
		facts, err := collector.Collect(ctx, tgt.Pod, namespace)
		if err != nil {
			return nil, fmt.Errorf("collecting pod facts: %w", err)
		}
		tgt.PodUnhealthy = target.IsUnhealthy(*facts.Pod)
		res.Facts = facts
		res.Findings = append(res.Findings, rules.RunAll(facts)...)
	}

	rules.AssignEvidenceIDs(res.Findings) // one E1…En sequence across all findings
	res.Target = tgt
	return res, nil
}

// Redacted returns copies of the pod facts and findings that are safe to send
// off the machine (to an LLM, an MCP client, or --dry-run output).
func (r *Result) Redacted() (*redact.RedactedFacts, []rules.Finding) {
	return redact.Redact(r.Facts), redact.Findings(r.Findings)
}
