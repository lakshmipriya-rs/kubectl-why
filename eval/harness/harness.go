// SPDX-License-Identifier: MIT

// This file holds the evaluation logic: each scenario's manifest is applied to
// its own namespace, the harness waits for the expected failure state, runs
// kubectl-why against the failing pod, and scores the JSON output.

package main

import (
	"bytes"
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
	Scenario           string   `json:"scenario"`
	Description        string   `json:"description"`
	ExpectedCategory   string   `json:"expected_category"`
	FixKeywords        []string `json:"fix_keywords"`
	MustNotHallucinate []string `json:"must_not_hallucinate"`
	// PodSelector is an optional label selector (e.g. "app=web") used when a
	// scenario creates several pods. Empty means any pod in the namespace.
	PodSelector string `json:"pod_selector,omitempty"`
	// Target is what to pass to kubectl-why, e.g. "deploy/web". Empty means
	// the failing pod found by WaitFor.
	Target  string   `json:"target,omitempty"`
	WaitFor WaitSpec `json:"wait_for"`
}

// WaitSpec describes what state the pod must reach before scoring.
// Every non-empty field must match. ExitCode 0 means "don't check".
type WaitSpec struct {
	PodPhase                  string `json:"pod_phase,omitempty"`
	ContainerWaitingReason    string `json:"container_waiting_reason,omitempty"`
	ContainerTerminatedReason string `json:"container_terminated_reason,omitempty"`
	ExitCode                  int    `json:"exit_code,omitempty"`
	EventReason               string `json:"event_reason,omitempty"`
	// MinRestarts: some container has restarted at least this many times.
	// More reliable than a waiting reason for crash loops, whose reported
	// state differs between Kubernetes versions.
	MinRestarts int `json:"min_restarts,omitempty"`
}

// ScoreResult holds per-scenario scoring.
type ScoreResult struct {
	Scenario        string
	Pod             string
	CategoryCorrect bool
	FixScore        int // number of fix_keywords present
	FixTotal        int
	MissingKeywords []string // fix_keywords not found in the output
	Hallucinations  []string
	Score           float64 // 0.0 – 1.0
	Error           string
}

// Passed reports whether the scenario ran cleanly, found the expected
// category and reported nothing from the must-not-hallucinate list.
func (r ScoreResult) Passed() bool {
	return r.Error == "" && r.CategoryCorrect && len(r.Hallucinations) == 0
}

// Options controls how scenarios are run.
type Options struct {
	ScenariosDir string
	Binary       string
	UseLLM       bool          // when true, kubectl-why runs with --explain
	Timeout      time.Duration // per-scenario wait for the failure state
	Filter       string        // run only scenarios whose directory name contains this
}

// RunAll executes all scenarios under opts.ScenariosDir and returns scores.
func RunAll(ctx context.Context, opts Options) []ScoreResult {
	entries, err := os.ReadDir(opts.ScenariosDir)
	if err != nil {
		return []ScoreResult{{Error: fmt.Sprintf("cannot read scenarios dir: %v", err)}}
	}

	var results []ScoreResult
	for _, e := range entries {
		if !e.IsDir() || !strings.Contains(e.Name(), opts.Filter) {
			continue
		}
		fmt.Fprintf(os.Stderr, "=== %s\n", e.Name())
		results = append(results, runScenario(ctx, filepath.Join(opts.ScenariosDir, e.Name()), opts))
	}
	return results
}

func runScenario(ctx context.Context, dir string, opts Options) ScoreResult {
	res := ScoreResult{Scenario: filepath.Base(dir)}

	gtData, err := os.ReadFile(filepath.Join(dir, "ground-truth.json"))
	if err != nil {
		res.Error = fmt.Sprintf("missing ground-truth.json: %v", err)
		return res
	}
	var gt GroundTruth
	if err := json.Unmarshal(gtData, &gt); err != nil {
		res.Error = fmt.Sprintf("invalid ground-truth.json: %v", err)
		return res
	}

	// Each scenario gets its own namespace so pods from other scenarios (or
	// anything else in "default") can never be picked by mistake.
	ns := "why-eval-" + strings.ToLower(res.Scenario)
	if err := ensureNamespace(ctx, ns); err != nil {
		res.Error = fmt.Sprintf("create namespace: %v", err)
		return res
	}
	defer func() {
		_, _ = kubectl(context.Background(), "delete", "namespace", ns, "--ignore-not-found", "--wait=false")
	}()

	if out, err := kubectl(ctx, "apply", "-n", ns, "-f", filepath.Join(dir, "manifest.yaml")); err != nil {
		res.Error = fmt.Sprintf("kubectl apply failed: %v: %s", err, out)
		return res
	}

	podName, err := waitForFailure(ctx, gt, ns, opts.Timeout)
	if err != nil {
		res.Error = fmt.Sprintf("wait for failure: %v", err)
		return res
	}
	res.Pod = podName

	whyTarget := podName
	if gt.Target != "" {
		whyTarget = gt.Target
	}
	output, err := runWhy(ctx, opts, whyTarget, ns)
	if err != nil {
		res.Error = fmt.Sprintf("kubectl-why failed: %v", err)
		return res
	}

	return score(res, gt, output)
}

// whyOutput is the subset of kubectl-why's JSON output the harness reads.
type whyOutput struct {
	Findings []struct {
		Category string `json:"category"`
	} `json:"findings"`
}

// score evaluates kubectl-why's JSON output against ground truth.
func score(res ScoreResult, gt GroundTruth, output string) ScoreResult {
	var out whyOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		res.Error = fmt.Sprintf("output is not valid JSON: %v", err)
		return res
	}

	// Category correctness: one of the reported findings must have exactly
	// the expected category (not merely mention it somewhere in the text).
	for _, f := range out.Findings {
		if strings.EqualFold(f.Category, gt.ExpectedCategory) {
			res.CategoryCorrect = true
		}
	}

	lower := strings.ToLower(output)

	res.FixTotal = len(gt.FixKeywords)
	for _, kw := range gt.FixKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			res.FixScore++
		} else {
			res.MissingKeywords = append(res.MissingKeywords, kw)
		}
	}

	for _, h := range gt.MustNotHallucinate {
		if strings.Contains(lower, strings.ToLower(h)) {
			res.Hallucinations = append(res.Hallucinations, h)
		}
	}

	// Composite score: category=1pt, each fix keyword=0.5pt, each hallucination=-1pt.
	maxPts := 1.0 + float64(res.FixTotal)*0.5
	pts := float64(res.FixScore) * 0.5
	if res.CategoryCorrect {
		pts += 1.0
	}
	pts -= float64(len(res.Hallucinations))
	if pts < 0 {
		pts = 0
	}
	res.Score = pts / maxPts
	return res
}

// PrintTable writes a summary table to stdout.
func PrintTable(results []ScoreResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SCENARIO\tRESULT\tCATEGORY_OK\tFIX\tHALLUC\tSCORE\tNOTES")
	for _, r := range results {
		result := "PASS"
		if !r.Passed() {
			result = "FAIL"
		}
		cat := "✓"
		if !r.CategoryCorrect {
			cat = "✗"
		}
		halluc := "0"
		if len(r.Hallucinations) > 0 {
			halluc = fmt.Sprintf("%d (%s)", len(r.Hallucinations), strings.Join(r.Hallucinations, ", "))
		}
		notes := r.Error
		if notes == "" && len(r.MissingKeywords) > 0 {
			notes = "missing keywords: " + strings.Join(r.MissingKeywords, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%s\t%.2f\t%s\n",
			r.Scenario, result, cat, r.FixScore, r.FixTotal, halluc, r.Score, notes)
	}
	w.Flush()
}

// ── Pod state matching ───────────────────────────────────────────────────────

// Minimal shapes of `kubectl get pods -o json`, enough for WaitSpec matching.
type podList struct {
	Items []pod `json:"items"`
}

type pod struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase                 string            `json:"phase"`
		ContainerStatuses     []containerStatus `json:"containerStatuses"`
		InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
	} `json:"status"`
}

type containerStatus struct {
	Name         string         `json:"name"`
	RestartCount int            `json:"restartCount"`
	State        containerState `json:"state"`
	LastState    containerState `json:"lastState"`
}

type containerState struct {
	Waiting *struct {
		Reason string `json:"reason"`
	} `json:"waiting"`
	Terminated *struct {
		Reason   string `json:"reason"`
		ExitCode int    `json:"exitCode"`
	} `json:"terminated"`
}

// matchesPodState reports whether p satisfies every pod-level condition in ws.
// EventReason is checked separately because it needs another API call.
// Container conditions match if ANY container (or init container) satisfies
// them; terminated reason and exit code must match on the same termination.
func matchesPodState(ws WaitSpec, p pod) bool {
	if ws.PodPhase != "" && !strings.EqualFold(p.Status.Phase, ws.PodPhase) {
		return false
	}

	statuses := append(append([]containerStatus{}, p.Status.ContainerStatuses...), p.Status.InitContainerStatuses...)

	if ws.ContainerWaitingReason != "" {
		found := false
		for _, cs := range statuses {
			if cs.State.Waiting != nil && strings.EqualFold(cs.State.Waiting.Reason, ws.ContainerWaitingReason) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if ws.MinRestarts > 0 {
		found := false
		for _, cs := range statuses {
			if cs.RestartCount >= ws.MinRestarts {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if ws.ContainerTerminatedReason != "" || ws.ExitCode != 0 {
		found := false
		for _, cs := range statuses {
			// Check the current state and the previous one (restarted containers).
			for _, st := range []containerState{cs.State, cs.LastState} {
				t := st.Terminated
				if t == nil {
					continue
				}
				if ws.ContainerTerminatedReason != "" && !strings.EqualFold(t.Reason, ws.ContainerTerminatedReason) {
					continue
				}
				if ws.ExitCode != 0 && t.ExitCode != ws.ExitCode {
					continue
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// waitForFailure polls the scenario namespace until a pod reaches the expected
// failure state and returns its name.
func waitForFailure(ctx context.Context, gt GroundTruth, ns string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lastSeen := "no pods yet"
	for {
		args := []string{"get", "pods", "-n", ns, "-o", "json"}
		if gt.PodSelector != "" {
			args = append(args, "-l", gt.PodSelector)
		}
		if out, err := kubectl(ctx, args...); err == nil {
			var pods podList
			if err := json.Unmarshal(out, &pods); err == nil {
				for _, p := range pods.Items {
					lastSeen = describePod(p)
					if !matchesPodState(gt.WaitFor, p) {
						continue
					}
					if gt.WaitFor.EventReason != "" && !podHasEvent(ctx, ns, p.Metadata.Name, gt.WaitFor.EventReason) {
						continue
					}
					return p.Metadata.Name, nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timed out after %v (last seen: %s)", timeout, lastSeen)
		case <-time.After(3 * time.Second):
		}
	}
}

func podHasEvent(ctx context.Context, ns, podName, reason string) bool {
	out, err := kubectl(ctx, "get", "events", "-n", ns,
		"--field-selector", "involvedObject.name="+podName,
		"-o", "jsonpath={.items[*].reason}")
	if err != nil {
		return false
	}
	for _, r := range strings.Fields(string(out)) {
		if r == reason {
			return true
		}
	}
	return false
}

// describePod gives a one-line summary for timeout messages.
func describePod(p pod) string {
	s := p.Metadata.Name + " phase=" + p.Status.Phase
	for _, cs := range p.Status.ContainerStatuses {
		switch {
		case cs.State.Waiting != nil:
			s += fmt.Sprintf(" %s=waiting:%s", cs.Name, cs.State.Waiting.Reason)
		case cs.State.Terminated != nil:
			s += fmt.Sprintf(" %s=terminated:%s:%d", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
		}
	}
	return s
}

// ── Command helpers ──────────────────────────────────────────────────────────

// kubectl runs a kubectl command and returns stdout. On failure the error
// includes stderr.
func kubectl(ctx context.Context, args ...string) ([]byte, error) {
	return runCmd(ctx, nil, "kubectl", args...)
}

// ensureNamespace creates ns, or does nothing if it already exists
// (e.g. left over from an interrupted run).
func ensureNamespace(ctx context.Context, ns string) error {
	manifest, err := kubectl(ctx, "create", "namespace", ns, "--dry-run=client", "-o", "yaml")
	if err != nil {
		return err
	}
	_, err = runCmd(ctx, manifest, "kubectl", "apply", "-f", "-")
	return err
}

// runWhy executes kubectl-why and returns its stdout (the JSON report).
// Stderr (warnings) is kept out of the JSON and only shown on failure.
func runWhy(ctx context.Context, opts Options, whyTarget, ns string) (string, error) {
	args := []string{whyTarget, "-n", ns, "--output", "json"}
	if opts.UseLLM {
		args = append(args, "--explain")
	}
	out, err := runCmd(ctx, nil, opts.Binary, args...)
	return string(out), err
}

func runCmd(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
