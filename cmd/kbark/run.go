// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/tui"
)

var profileFlag string

func init() {
	rootCmd.PersistentFlags().StringVar(
		&profileFlag,
		"profile",
		defaultProfile(),
		"Configuration profile (dev/staging/prod)",
	)
	// Running `kbark` with no subcommand launches the TUI. Subcommands
	// like `kbark version` and `kbark doctor` continue to work as before.
	rootCmd.RunE = runTUI
}

// defaultProfile honours KBARK_PROFILE so the user can change profile
// without re-typing the flag every invocation.
func defaultProfile() string {
	if v := os.Getenv("KBARK_PROFILE"); v != "" {
		return v
	}
	return "dev"
}

func runTUI(_ *cobra.Command, _ []string) error {
	model := tui.NewModel(kubeFlags, profileFlag)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui exited: %w", err)
	}
	return nil
}
