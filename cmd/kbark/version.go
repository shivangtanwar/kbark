// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the kbark version, commit, and build date",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("kbark %s\n  commit: %s\n  built:  %s\n",
			version.Version, version.Commit, version.Date)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
