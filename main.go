// SPDX-License-Identifier: MIT
// kubectl-why: diagnose a single failing pod and print a root-cause report.

package main

import (
	"os"

	"github.com/lakshmipriya-rs/kubectl-why/cmd"
)

// Set at build time by goreleaser via -ldflags "-X main.version=...".
// Builds without ldflags fall back to Go module/VCS build info.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
