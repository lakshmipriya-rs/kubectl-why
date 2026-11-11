// SPDX-License-Identifier: MIT

// Package harness applies broken Kubernetes manifests to a kind cluster,
// waits for the expected failure state, runs kubectl-why, and scores the output.
//
// Usage (requires a running kind cluster and kubectl-why binary in PATH):
//
//	go run ./eval/harness --scenarios ./eval/scenarios --binary kubectl-why
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// GroundTruth is loaded from each scenario's ground-truth.json.
type GroundTruth struct {
	Scenario            string   `json:"scenario"`
	Description         string   `json:"description"`
	ExpectedCategory    string   `json:"expected_category"`
	FixKeywords         []string `json:"fix_keywords"`
	MustNotHallucinate  []string `json:"must_not_hallucinate"`
	WaitFor             WaitSpec `json:"wait_for"`
}

// WaitSpec describes what state the pod must reach before scoring.
type WaitSpec struct {
	PodPhase                  string `json:"pod_phase,omitempty"`
	ContainerWaitingReason    string `json:"container_waiting_reason,omitempty"`
	ContainerTerminatedReason string `json:"container_terminated_reason,omitempty"`
	ExitCode                  int    `json:"exit_code,omitempty"`
	EventReason               string `json:"event_reason,omitempty"`
}

// ScoreResult holds per-scenario scoring.
type ScoreResult struct {
	Scenario        string
	CategoryCorrect bool
	FixScore        int // number of fix_keywords present
	FixTotal        int
	Hallucinations  int
	Score           float64 // 0.0 – 1.0
	Error           string
}

// RunAll executes all scenarios under scenariosDir and returns scores.
func RunAll(ctx context.Context, scenariosDir, binary string) []ScoreResult {
	entries, err := os.ReadDir(scenariosDir)
	if err != nil {
		return []ScoreResult{{Error: fmt.Sprintf("cannot read scenarios dir: %v", err)}}
	}

	var results []ScoreResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(scenariosDir, e.Name())
		result := runScenario(ctx, dir, binary)
		results = append(results, result)
	}
	return results
}

func runScenario(ctx context.Context, dir, binary string) ScoreResult {
	res := ScoreResult{Scenario: filepath.Base(dir)}

	// Load ground truth.
	gtPath := filepath.Join(dir, "ground-truth.json")
	gtData, err := os.ReadFile(gtPath)
	if err != nil {
		res.Error = fmt.Sprintf("missing ground-truth.json: %v", err)
		return res
	}
	var gt GroundTruth
	if err := json.Unmarshal(gtData, &gt); err != nil {
		res.Error = fmt.Sprintf("invalid ground-truth.json: %v", err)
		return res
	}

	// Apply manifest.
	manifest := filepath.Join(dir, "manifest.yaml")
	if err := kubectl(ctx, "apply", "-f", manifest); err != nil {
		res.Error = fmt.Sprintf("kubectl apply failed: %v", err)
		return res
	}
	// Best-effort cleanup after scoring.
	defer func() { _ = kubectl(context.Background(), "delete", "-f", manifest, "--ignore-not-found") }()

	// Determine the pod name to diagnose.
	podName, ns, err := waitForFailure(ctx, &gt, 120*time.Second)
	if err != nil {
		res.Error = fmt.Sprintf("wait for failure: %v", err)
		return res
	}

	// Run kubectl-why.
	output, err := runWhy(ctx, binary, podName, ns)
	if err != nil {
		res.Error = fmt.Sprintf("kubectl-why failed: %v", err)
		return res
	}

	// Score.
	return score(res, gt, output)
}

// score evaluates the JSON output against ground truth.
func score(res ScoreResult, gt GroundTruth, output string) ScoreResult {
	lower := strings.ToLower(output)

	// Category correctness.
	res.CategoryCorrect = strings.Contains(lower, strings.ToLower(gt.ExpectedCategory))

	// Fix keywords.
	res.FixTotal = len(gt.FixKeywords)
	for _, kw := range gt.FixKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			res.FixScore++
		}
	}

	// Hallucinations: must-not-appear terms.
	for _, h := range gt.MustNotHallucinate {
		if strings.Contains(lower, strings.ToLower(h)) {
			res.Hallucinations++
		}
	}

	// Composite score: category=1pt, each fix kw=0.5pt, each hallucination=-1pt
	pts := 0.0
	max := 1.0 + float64(res.FixTotal)*0.5
	if res.CategoryCorrect {
		pts += 1.0
	}
	pts += float64(res.FixScore) * 0.5
	pts -= float64(res.Hallucinations)
	if pts < 0 {
		pts = 0
	}
	res.Score = pts / max
	return res
}

// PrintTable writes a summary table to stdout.
func PrintTable(results []ScoreResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SCENARIO\tCATEGORY_OK\tFIX\tHALLUC\tSCORE\tERROR")
	for _, r := range results {
		fixStr := fmt.Sprintf("%d/%d", r.FixScore, r.FixTotal)
		scoreStr := fmt.Sprintf("%.2f", r.Score)
		catStr := "✓"
		if !r.CategoryCorrect {
			catStr = "✗"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			r.Scenario, catStr, fixStr, r.Hallucinations, scoreStr, r.Error)
	}
	w.Flush()
}

// kubectl runs a kubectl command and returns combined output or error.
func kubectl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stderr // progress to stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runWhy executes the kubectl-why binary and captures its JSON output.
func runWhy(ctx context.Context, binary, podName, ns string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, podName, "-n", ns, "--output", "json", "--no-llm")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// waitForFailure polls until the expected failure state appears, then returns
// the pod name and namespace to diagnose.
func waitForFailure(ctx context.Context, gt *GroundTruth, timeout time.Duration) (string, string, error) {
	deadline := time.Now().Add(timeout)
	ns := "default"

	// Determine expected pod name from scenario (simple heuristic: first pod with matching label).
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		out, err := exec.CommandContext(ctx, "kubectl", "get", "pods", "-n", ns,
			"-o", "jsonpath={range .items[*]}{.metadata.name} {.status.phase} {.status.containerStatuses[0].state.waiting.reason}{\"\\n\"}{end}").Output()
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		for _, line := range strings.Split(string(out), "\n") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			podName := parts[0]
			phase := parts[1]
			waitReason := ""
			if len(parts) >= 3 {
				waitReason = parts[2]
			}

			// Match by expected conditions.
			if gt.WaitFor.PodPhase != "" && !strings.EqualFold(phase, gt.WaitFor.PodPhase) {
				continue
			}
			if gt.WaitFor.ContainerWaitingReason != "" && !strings.EqualFold(waitReason, gt.WaitFor.ContainerWaitingReason) {
				continue
			}
			if gt.WaitFor.EventReason != "" {
				evtOut, _ := exec.CommandContext(ctx, "kubectl", "get", "events",
					"-n", ns, "--field-selector", "involvedObject.name="+podName,
					"-o", "jsonpath={.items[*].reason}").Output()
				if !strings.Contains(string(evtOut), gt.WaitFor.EventReason) {
					continue
				}
			}
			return podName, ns, nil
		}
		time.Sleep(5 * time.Second)
	}
	return "", "", fmt.Errorf("timed out waiting for failure state after %v", timeout)
}
