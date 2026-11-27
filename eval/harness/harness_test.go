// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustPod(t *testing.T, js string) pod {
	t.Helper()
	var p pod
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		t.Fatalf("bad pod JSON: %v", err)
	}
	return p
}

const oomPod = `{
  "metadata": {"name": "oomkilled-pod"},
  "status": {
    "phase": "Failed",
    "containerStatuses": [
      {"name": "stress", "state": {"terminated": {"reason": "OOMKilled", "exitCode": 137}}}
    ]
  }
}`

const pullPod = `{
  "metadata": {"name": "bad-image-abc"},
  "status": {
    "phase": "Pending",
    "containerStatuses": [
      {"name": "sidecar", "state": {"running": {}}},
      {"name": "app", "state": {"waiting": {"reason": "ImagePullBackOff"}}}
    ]
  }
}`

const restartedPod = `{
  "metadata": {"name": "restarted"},
  "status": {
    "phase": "Running",
    "containerStatuses": [
      {"name": "app", "restartCount": 2, "state": {"running": {}},
       "lastState": {"terminated": {"reason": "Error", "exitCode": 137}}}
    ]
  }
}`

func TestMatchesPodState(t *testing.T) {
	tests := []struct {
		name string
		ws   WaitSpec
		pod  string
		want bool
	}{
		{"terminated reason and exit code", WaitSpec{ContainerTerminatedReason: "OOMKilled", ExitCode: 137}, oomPod, true},
		{"wrong terminated reason", WaitSpec{ContainerTerminatedReason: "Error"}, oomPod, false},
		{"wrong exit code", WaitSpec{ExitCode: 1}, oomPod, false},
		{"waiting reason on second container", WaitSpec{PodPhase: "Pending", ContainerWaitingReason: "ImagePullBackOff"}, pullPod, true},
		{"wrong phase", WaitSpec{PodPhase: "Running", ContainerWaitingReason: "ImagePullBackOff"}, pullPod, false},
		{"waiting reason absent", WaitSpec{ContainerWaitingReason: "CrashLoopBackOff"}, pullPod, false},
		{"lastState termination counts", WaitSpec{ContainerTerminatedReason: "Error", ExitCode: 137}, restartedPod, true},
		{"min restarts met", WaitSpec{MinRestarts: 2, ExitCode: 137}, restartedPod, true},
		{"min restarts not met", WaitSpec{MinRestarts: 3}, restartedPod, false},
		// Regression: an empty WaitSpec used to make the old harness match any pod.
		// It still matches (nothing to check), which is why each scenario now
		// runs in its own namespace.
		{"empty spec matches", WaitSpec{}, pullPod, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesPodState(tc.ws, mustPod(t, tc.pod)); got != tc.want {
				t.Errorf("matchesPodState = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScore(t *testing.T) {
	gt := GroundTruth{
		ExpectedCategory:   "OOMKilled",
		FixKeywords:        []string{"memory", "limit"},
		MustNotHallucinate: []string{"ImagePullBackOff"},
	}

	t.Run("correct", func(t *testing.T) {
		out := `{"findings":[{"category":"OOMKilled","detail":"increase the memory limit"}]}`
		r := score(ScoreResult{}, gt, out)
		if !r.Passed() || r.FixScore != 2 || r.Score != 1.0 || len(r.MissingKeywords) != 0 {
			t.Errorf("unexpected result: %+v", r)
		}
	})

	t.Run("missing keywords are listed", func(t *testing.T) {
		out := `{"findings":[{"category":"OOMKilled","detail":"raise it"}]}`
		r := score(ScoreResult{}, gt, out)
		if len(r.MissingKeywords) != 2 || r.MissingKeywords[0] != "memory" || r.MissingKeywords[1] != "limit" {
			t.Errorf("MissingKeywords = %v, want [memory limit]", r.MissingKeywords)
		}
	})

	t.Run("category only mentioned in text is not enough", func(t *testing.T) {
		out := `{"findings":[{"category":"SIGKILL","detail":"not OOMKilled, check memory limit"}]}`
		r := score(ScoreResult{}, gt, out)
		if r.CategoryCorrect {
			t.Error("category should come from the findings list, not free text")
		}
		if r.Passed() {
			t.Error("expected FAIL")
		}
	})

	t.Run("hallucination fails the scenario", func(t *testing.T) {
		out := `{"findings":[{"category":"OOMKilled"},{"category":"ImagePullBackOff"}]}`
		r := score(ScoreResult{}, gt, out)
		if len(r.Hallucinations) != 1 || r.Passed() {
			t.Errorf("expected one hallucination and FAIL, got %+v", r)
		}
	})

	t.Run("invalid JSON is an error", func(t *testing.T) {
		r := score(ScoreResult{}, gt, "warning: something\n{")
		if r.Error == "" {
			t.Error("expected error for non-JSON output")
		}
	})
}

// Every scenario must have a parseable ground truth with a category and at
// least one wait condition, and its manifest must not pin a namespace
// (the harness applies it into a per-scenario namespace).
func TestScenarioFiles(t *testing.T) {
	dirs, err := filepath.Glob("../scenarios/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no scenarios found: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, "ground-truth.json"))
			if err != nil {
				t.Fatal(err)
			}
			var gt GroundTruth
			if err := json.Unmarshal(data, &gt); err != nil {
				t.Fatalf("invalid ground-truth.json: %v", err)
			}
			if gt.ExpectedCategory == "" {
				t.Error("expected_category is empty")
			}
			if gt.WaitFor == (WaitSpec{}) {
				t.Error("wait_for has no conditions")
			}
			manifest, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if containsLine(string(manifest), "namespace:") {
				t.Error("manifest must not set metadata.namespace")
			}
		})
	}
}

func containsLine(s, prefix string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}
