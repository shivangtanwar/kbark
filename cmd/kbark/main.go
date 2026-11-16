// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/version"
)

var rootCmd = &cobra.Command{
	Use:   "kbark",
	Short: "kbark — Kubernetes failures detected, explained and fixed with approval",
	Long: `kbark is two tools in one binary.

  kbark          A terminal UI in the spirit of k9s. Press ? on any pod,
                 deployment, log line, or event for an inline AI diagnosis.
                 Read-only.

  kbark serve    Watches every pod in one or more clusters, opens an
                 incident for each failure, proposes a typed fix, and
                 applies it only after a named person approves it on the
                 dashboard.

Single binary. Bring your own AI key; detection and fixes work without one.`,
	Version:       fmt.Sprintf("%s (commit %s, built %s)", version.Version, version.Commit, version.Date),
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	// Cobra's default --version template prints "kbark version <ver>".
	// Match the kbark version subcommand's brief form instead so the
	// flag and subcommand are visually consistent.
	rootCmd.SetVersionTemplate("kbark {{.Version}}\n")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "kbark:", err)
		os.Exit(1)
	}
}
