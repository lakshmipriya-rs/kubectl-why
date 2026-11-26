// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"
	"regexp"
	"runtime/debug"
)

// pseudoVersion matches Go pseudo-versions such as
// v0.0.0-20261126234418-69c694e39f44 (Go 1.24+ stamps these on local builds).
var pseudoVersion = regexp.MustCompile(`-(0\.)?\d{14}-[0-9a-f]{12}`)

// SetVersionInfo is called from main with values injected at build time by
// goreleaser (-ldflags "-X main.version=..."). It enables `kubectl why --version`.
func SetVersionInfo(version, commit, date string) {
	rootCmd.Version = formatVersion(version, commit, date, readBuildInfo())
	rootCmd.SetVersionTemplate("kubectl-why {{.Version}}\n")
}

// buildInfo is the subset of runtime/debug.BuildInfo used as a fallback when
// the binary was not built by goreleaser (e.g. `go install` or `go build`).
type buildInfo struct {
	moduleVersion string // "v0.2.0" for `go install ...@v0.2.0`, "(devel)" for local builds
	revision      string // vcs.revision
	time          string // vcs.time
	modified      bool   // vcs.modified (uncommitted changes)
}

func readBuildInfo() buildInfo {
	var bi buildInfo
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return bi
	}
	bi.moduleVersion = info.Main.Version
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			bi.revision = s.Value
		case "vcs.time":
			bi.time = s.Value
		case "vcs.modified":
			bi.modified = s.Value == "true"
		}
	}
	return bi
}

// formatVersion prefers ldflags values and falls back to Go build info.
func formatVersion(version, commit, date string, bi buildInfo) string {
	if version == "" || version == "dev" {
		version = "dev"
		// Use a real release tag (from `go install ...@v0.2.0`); a pseudo-version
		// adds nothing that the commit below doesn't already say.
		if bi.moduleVersion != "" && bi.moduleVersion != "(devel)" && !pseudoVersion.MatchString(bi.moduleVersion) {
			version = bi.moduleVersion
		}
	}
	if commit == "" || commit == "none" {
		commit = shortHash(bi.revision)
		if commit != "" && bi.modified {
			commit += "-dirty"
		}
	} else {
		commit = shortHash(commit)
	}
	if date == "" || date == "unknown" {
		date = bi.time
	}

	out := version
	switch {
	case commit != "" && date != "":
		out += fmt.Sprintf(" (commit %s, built %s)", commit, date)
	case commit != "":
		out += fmt.Sprintf(" (commit %s)", commit)
	}
	return out
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
