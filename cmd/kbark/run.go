// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/shivangtanwar/kbark/internal/kube"
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
	rootCmd.RunE = runTUI
}

func defaultProfile() string {
	if v := os.Getenv("KBARK_PROFILE"); v != "" {
		return v
	}
	return "dev"
}

func runTUI(_ *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	clientset, err := kube.NewClientset(kubeFlags)
	if err != nil {
		return fmt.Errorf("build kube client: %w", err)
	}

	namespace, _, _ := kubeFlags.ToRawKubeConfigLoader().Namespace()
	service := kube.NewPodService(clientset, kube.DefaultResyncInterval, ctx)
	snapshots, done, err := service.Switch(namespace)
	if err != nil {
		return fmt.Errorf("start pod informer: %w", err)
	}

	model := tui.NewModel(kubeFlags, profileFlag, service, snapshots, done)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui exited: %w", err)
	}
	return nil
}
