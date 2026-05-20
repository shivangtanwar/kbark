// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/kube"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check kbark's prerequisites (kubeconfig, apiserver, AI providers)",
	Long: `kbark doctor reports the state of every prerequisite kbark needs:
the kubeconfig file, the apiserver, and the configured AI providers.

In this build only the kubeconfig and apiserver are checked; AI provider
checks are added in a subsequent change.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		clientset, err := kube.NewClientset(kubeFlags)
		if err != nil {
			fmt.Printf("kubeconfig: FAIL  %v\n", err)
			return err
		}
		fmt.Println("kubeconfig: OK")

		info, err := clientset.Discovery().ServerVersion()
		if err != nil {
			fmt.Printf("apiserver:  FAIL  %v\n", err)
			return err
		}
		fmt.Printf("apiserver:  OK    %s on %s\n", info.GitVersion, info.Platform)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
