// SPDX-License-Identifier: Apache-2.0

// Package version exposes build-time identifiers populated via -ldflags -X.
// They are deliberately variables (not constants) so goreleaser / Taskfile
// can override them at link time without touching source.
package version

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
