// SPDX-License-Identifier: MIT

// eval/harness/main.go is the entry point for the evaluation harness.
// It applies each scenario to a kind cluster, runs kubectl-why, and scores results.
//
// Prerequisites:
//   - A running kind cluster (kubectl context pointing at it)
//   - kubectl-why binary in PATH or supplied via --binary
//
// Example:
//
//	go run ./eval/harness --scenarios ./eval/scenarios
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

)

func main() {
	scenariosDir := flag.String("scenarios", "eval/scenarios", "Path to scenarios directory")
	binary := flag.String("binary", "kubectl-why", "Path to kubectl-why binary")
	flag.Parse()

	ctx := context.Background()
	results := RunAll(ctx, *scenariosDir, *binary)
	PrintTable(results)

	// Exit non-zero if any scenario errored.
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(os.Stderr, "scenario %s failed: %s\n", r.Scenario, r.Error)
			os.Exit(1)
		}
	}
}
