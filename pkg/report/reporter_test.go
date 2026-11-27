// SPDX-License-Identifier: MIT

package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/report"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/target"
)

func TestReporter_TextMode(t *testing.T) {
	var buf bytes.Buffer
	r := report.New(&buf)

	findings := []rules.Finding{
		{
			Category: rules.CategoryOOMKilled,
			Detail:   "Container killed by OOM",
			Evidence: []rules.Evidence{{
				ID: "E1", Source: "Pod/oom", Field: "status.containerStatuses[app].state.terminated.reason", Value: "OOMKilled",
			}},
			Severity: "critical",
		},
	}
	resp := &llm.Response{
		RootCause:   "Memory limit too low",
		CausalChain: []string{"app allocates 512Mi", "limit is 10Mi", "OOM killer fires"},
		EvidenceIDs: []string{"E1"},
		Fix:         "Increase memory limit to 512Mi",
		Confidence:  "high",
	}

	if err := r.Print(report.Result{Findings: findings, LLM: resp}, "text"); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "OOMKilled") {
		t.Error("expected OOMKilled in output")
	}
	if !strings.Contains(out, "Memory limit too low") {
		t.Error("expected LLM root cause in output")
	}
	if !strings.Contains(out, "Increase memory limit") {
		t.Error("expected fix in output")
	}
	if !strings.Contains(out, "[E1] Pod/oom status.containerStatuses[app].state.terminated.reason: OOMKilled") {
		t.Errorf("expected evidence with ID in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Cited evidence:\n  • [E1] Pod/oom") {
		t.Errorf("expected cited evidence resolved by ID, got:\n%s", out)
	}
}

func TestReporter_UncitedLLMClaimIsFlagged(t *testing.T) {
	var buf bytes.Buffer
	resp := &llm.Response{RootCause: "Something", Confidence: "low"}
	if err := report.New(&buf).Print(report.Result{LLM: resp}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not backed by collected evidence") {
		t.Errorf("expected uncited warning, got:\n%s", buf.String())
	}
}

func TestReporter_JSONMode(t *testing.T) {
	var buf bytes.Buffer
	r := report.New(&buf)

	findings := []rules.Finding{
		{Category: rules.CategoryImagePullBackOff, Severity: "critical"},
	}
	if err := r.Print(report.Result{Findings: findings, LLM: &llm.Response{RootCause: "bad tag"}}, "json"); err != nil {
		t.Fatal(err)
	}

	var result report.Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, buf.String())
	}
	if len(result.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.LLM == nil || result.LLM.RootCause != "bad tag" {
		t.Error("LLM root cause missing from JSON")
	}
}

func TestReporter_NoFindings(t *testing.T) {
	var buf bytes.Buffer
	r := report.New(&buf)
	if err := r.Print(report.Result{LLM: &llm.Response{}}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No problems detected") {
		t.Error("expected no-problems message")
	}
}

func TestReporter_WorkloadTargetHeader(t *testing.T) {
	var buf bytes.Buffer
	tgt := &target.Target{Kind: target.KindDeployment, Name: "web", Pod: "web-abc", PodsTotal: 3, PodsUnhealthy: 2}
	if err := report.New(&buf).Print(report.Result{Target: tgt}, "text"); err != nil {
		t.Fatal(err)
	}
	want := "Diagnosing Deployment/web → pod web-abc (2 of 3 pods unhealthy)"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("expected %q in output, got:\n%s", want, buf.String())
	}
}

func TestReporter_JSONIncludesTarget(t *testing.T) {
	var buf bytes.Buffer
	tgt := &target.Target{Kind: target.KindPod, Name: "p", Pod: "p"}
	if err := report.New(&buf).Print(report.Result{Target: tgt}, "json"); err != nil {
		t.Fatal(err)
	}
	var result report.Result
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Target == nil || result.Target.Pod != "p" || result.Target.Kind != target.KindPod {
		t.Errorf("target missing from JSON: %s", buf.String())
	}
}

// Without --explain the LLM is never called (LLM == nil): suggest --explain.
// With --explain but nothing found, don't repeat the suggestion.
func TestReporter_NoFindingsHint(t *testing.T) {
	var buf bytes.Buffer
	if err := report.New(&buf).Print(report.Result{}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "--explain") {
		t.Errorf("expected --explain hint in rules-only mode, got:\n%s", buf.String())
	}

	buf.Reset()
	if err := report.New(&buf).Print(report.Result{LLM: &llm.Response{}}, "text"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "--explain") {
		t.Errorf("did not expect --explain hint when the LLM was already used, got:\n%s", buf.String())
	}
}

// Rules-only JSON output has no "llm" key at all.
func TestReporter_JSONOmitsLLMWhenNotUsed(t *testing.T) {
	var buf bytes.Buffer
	if err := report.New(&buf).Print(report.Result{}, "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"llm"`) {
		t.Errorf("expected no llm key, got: %s", buf.String())
	}
}

func TestReporter_HealthyPod(t *testing.T) {
	var buf bytes.Buffer
	tgt := &target.Target{Kind: target.KindPod, Name: "p", Pod: "p", PodUnhealthy: false}
	if err := report.New(&buf).Print(report.Result{Target: tgt}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "looks healthy") || strings.Contains(buf.String(), "--explain") {
		t.Errorf("expected a healthy message without the --explain hint, got:\n%s", buf.String())
	}
}
