// SPDX-License-Identifier: MIT
// kubectl-why: diagnose a single failing pod and print a root-cause report.

package main

import (
	"os"

	"github.com/lakshmipriya-rs/kubectl-why/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
