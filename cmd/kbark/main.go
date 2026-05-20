// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "kbark",
	Short: "kbark — Kubernetes TUI with an AI on the ? key",
	Long: `kbark is a Kubernetes terminal UI in the spirit of k9s.

Press ? on any pod, deployment, log line, or event to open an inline
AI diagnosis that pulls describe, events, and logs for you and explains
what's wrong in plain English. Read-only. Single binary. BYO key.`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "kbark:", err)
		os.Exit(1)
	}
}
