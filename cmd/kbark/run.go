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
	"k8s.io/client-go/dynamic"

	"github.com/shivangtanwar/kbark/internal/ai"
	"github.com/shivangtanwar/kbark/internal/diagnose"
	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
	"github.com/shivangtanwar/kbark/internal/tui"
)

var profileFlag string

const defaultAIModel = "claude-sonnet-4-6"

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
	podService := kube.NewPodService(clientset, kube.DefaultResyncInterval, ctx)
	podsCh, podsDone, err := podService.Switch(namespace)
	if err != nil {
		return fmt.Errorf("start pod informer: %w", err)
	}
	logService := kube.NewLogService(clientset, ctx)
	podContextBuilder := diagnose.NewPodContextBuilder(clientset)

	// Dynamic client powers the get_resource tool. If the rest config
	// build fails for some reason, the dispatcher gracefully reports a
	// helpful message instead of crashing on `?`.
	var dynClient dynamic.Interface
	if restCfg, err := kube.RESTConfig(kubeFlags); err == nil {
		if c, err := dynamic.NewForConfig(restCfg); err == nil {
			dynClient = c
		}
	}
	dispatcher := diagnose.NewDispatcher(clientset, dynClient)

	// AI is optional. If the env isn't set we let the TUI start and
	// surface the configuration error inside the diagnose modal when the
	// user actually presses `?`.
	var aiProvider ai.Provider
	if p, err := ai.New("anthropic"); err == nil {
		aiProvider = p
	}

	// Kind registry + per-kind resource services. Pods stay on the
	// legacy typed PodService path (the diagnose `?` flow needs
	// typed *corev1.Pod). The pod plugin is registered so M2.2 can
	// refactor PodView onto TableResourceView as a deletion job.
	registry := kinds.NewRegistry(
		kinds.Pods(),
		kinds.Deployments(),
		kinds.Services(),
	)
	resourceServices := map[string]*kube.ResourceService{}
	for _, key := range registry.Keys() {
		if key == "po" {
			continue
		}
		p, _ := registry.Lookup(key)
		resourceServices[key] = kube.NewResourceService(clientset, kube.DefaultResyncInterval, ctx, p)
	}

	model := tui.NewModel(tui.ModelDeps{
		Ctx:               ctx,
		Flags:             kubeFlags,
		Profile:           profileFlag,
		PodService:        podService,
		PodsCh:            podsCh,
		PodsDone:          podsDone,
		LogService:        logService,
		PodContextBuilder: podContextBuilder,
		ToolDispatcher:    dispatcher,
		AIProvider:        aiProvider,
		AIModel:           defaultAIModel,
		KindRegistry:      registry,
		ResourceServices:  resourceServices,
	})

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
