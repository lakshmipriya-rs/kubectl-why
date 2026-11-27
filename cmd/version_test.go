// SPDX-License-Identifier: MIT

package cmd

import "testing"

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		name                  string
		version, commit, date string
		bi                    buildInfo
		want                  string
	}{
		{
			name:    "goreleaser ldflags win",
			version: "0.2.0", commit: "69c694e1234567890", date: "2026-11-26T10:00:00Z",
			bi:   buildInfo{moduleVersion: "v9.9.9", revision: "other"},
			want: "0.2.0 (commit 69c694e12345, built 2026-11-26T10:00:00Z)",
		},
		{
			name:    "go install falls back to module version and vcs info",
			version: "dev", commit: "none", date: "unknown",
			bi:   buildInfo{moduleVersion: "v0.2.0", revision: "abcdef", time: "2026-11-26T10:00:00Z"},
			want: "v0.2.0 (commit abcdef, built 2026-11-26T10:00:00Z)",
		},
		{
			name:    "local build with uncommitted changes",
			version: "dev", commit: "none", date: "unknown",
			bi:   buildInfo{moduleVersion: "(devel)", revision: "abcdef", modified: true},
			want: "dev (commit abcdef-dirty)",
		},
		{
			name:    "pseudo-version from a local Go 1.24 build becomes dev",
			version: "dev", commit: "none", date: "unknown",
			bi:   buildInfo{moduleVersion: "v0.0.0-20261126234418-69c694e39f44+dirty", revision: "69c694e39f44", modified: true},
			want: "dev (commit 69c694e39f44-dirty)",
		},
		{
			name:    "no info at all",
			version: "dev", commit: "none", date: "unknown",
			want: "dev",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatVersion(tc.version, tc.commit, tc.date, tc.bi); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
