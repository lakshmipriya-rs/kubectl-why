// SPDX-License-Identifier: MIT

// Command harness runs the kubectl-why evaluation suite. It applies each
// scenario to its own namespace on the current cluster (normally kind), runs
// kubectl-why, and scores the results.
//
// Prerequisites:
//   - A running cluster (kubectl context pointing at it), e.g. `kind create cluster`
//   - kubectl-why binary in PATH or supplied via --binary
//
// Example:
//
//	go run ./eval/harness --scenarios ./eval/scenarios --binary ./kubectl-why
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

func main() {
	var opts Options
	flag.StringVar(&opts.ScenariosDir, "scenarios", "eval/scenarios", "Path to scenarios directory")
	flag.StringVar(&opts.Binary, "binary", "kubectl-why", "Path to kubectl-why binary")
	flag.BoolVar(&opts.UseLLM, "llm", false, "Let kubectl-why call its LLM provider (default: run with --no-llm)")
	flag.DurationVar(&opts.Timeout, "timeout", 3*time.Minute, "How long to wait for each scenario's failure state")
	flag.StringVar(&opts.Filter, "run", "", "Only run scenarios whose directory name contains this string")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	results := RunAll(ctx, opts)
	PrintTable(results)

	// Exit non-zero if any scenario failed, so CI can gate on the suite.
	failed := 0
	for _, r := range results {
		if !r.Passed() {
			failed++
		}
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no scenarios matched")
		os.Exit(1)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d of %d scenarios failed\n", failed, len(results))
		os.Exit(1)
	}
}
