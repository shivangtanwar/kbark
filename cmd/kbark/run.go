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
	"github.com/shivangtanwar/kbark/internal/config"
	"github.com/shivangtanwar/kbark/internal/describe"
	"github.com/shivangtanwar/kbark/internal/diagnose"
	"github.com/shivangtanwar/kbark/internal/kube"
	"github.com/shivangtanwar/kbark/internal/kube/kinds"
	"github.com/shivangtanwar/kbark/internal/tui"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

var profileFlag string

func init() {
	rootCmd.PersistentFlags().StringVar(
		&profileFlag,
		"profile",
		defaultProfile(),
		"Configuration profile name (loaded from ~/.config/kbark/config.yaml; built-in default: dev)",
	)
	rootCmd.RunE = runTUI
}

// defaultProfile picks the initial value for --profile. KBARK_PROFILE
// env takes precedence so users can switch profiles without touching
// shell history; the empty default falls through to config.Load's
// DefaultProfile lookup at runtime.
func defaultProfile() string {
	if v := os.Getenv("KBARK_PROFILE"); v != "" {
		return v
	}
	return ""
}

func runTUI(_ *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Resolve the active profile. A missing config file falls back to
	// built-in defaults (dev → anthropic + claude-sonnet); an unknown
	// --profile value errors out with the list of valid names so the
	// user can correct their invocation.
	cfgPath, _ := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	profile, err := cfg.Resolve(profileFlag)
	if err != nil {
		return err
	}
	activeProfileName := profileFlag
	if activeProfileName == "" {
		activeProfileName = cfg.DefaultProfile
	}

	clientset, err := kube.NewClientset(kubeFlags)
	if err != nil {
		return fmt.Errorf("build kube client: %w", err)
	}

	namespace, _, _ := kubeFlags.ToRawKubeConfigLoader().Namespace()
	logService := kube.NewLogService(clientset, ctx)
	podContextBuilder := diagnose.NewPodContextBuilder(clientset)
	logContextBuilder := diagnose.NewLogContextBuilder(clientset)
	// resourceContextBuilder wraps describeService (built later) — set
	// after that line.

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

	// AI provider is dictated by the active profile. If credentials
	// aren't set for that provider we let the TUI start and surface
	// the configuration error inside the diagnose modal on first `?`.
	var aiProvider ai.Provider
	if p, err := ai.New(profile.Provider); err == nil {
		aiProvider = p
	}

	// Kind registry + one ResourceService per kind. Pods are no
	// longer a special case — they go through the same plumbing as
	// every other kind, with "po" being the default home view.
	registry := kinds.NewRegistry(
		kinds.Pods(),
		kinds.Deployments(),
		kinds.Services(),
		kinds.ConfigMaps(),
		kinds.Secrets(),
		kinds.Ingresses(),
		kinds.StatefulSets(),
		kinds.DaemonSets(),
		kinds.Jobs(),
		kinds.CronJobs(),
		kinds.Events(),
		kinds.Nodes(),
	)
	resourceServices := map[string]*kube.ResourceService{}
	for _, key := range registry.Keys() {
		p, _ := registry.Lookup(key)
		resourceServices[key] = kube.NewResourceService(clientset, kube.DefaultResyncInterval, ctx, p)
	}

	// Pre-Switch the home kind so the TUI's first paint shows live
	// data instead of an empty table.
	const homeKind = "po"
	homeCh, homeDone, err := resourceServices[homeKind].Switch(namespace)
	if err != nil {
		return fmt.Errorf("start %s informer: %w", homeKind, err)
	}

	// kubectl/describe via ConfigFlags as the RESTClientGetter.
	// kubeFlags already implements the interface, so the modal lights
	// up with no extra wiring.
	describeService := describe.NewService(kubeFlags)

	// `?`-on-non-pod payload builder. Shares describeService so the
	// AI flow sees the same kubectl-style output the modal does.
	resourceContextBuilder := diagnose.NewResourceContextBuilder(describeService)

	model := tui.NewModel(tui.ModelDeps{
		Ctx:                    ctx,
		Flags:                  kubeFlags,
		Profile:                activeProfileName,
		LogService:             logService,
		PodContextBuilder:      podContextBuilder,
		LogContextBuilder:      logContextBuilder,
		ResourceContextBuilder: resourceContextBuilder,
		ToolDispatcher:         dispatcher,
		AIProvider:             aiProvider,
		AIModel:                profile.Model,
		TokenBudget:            profile.TokenBudget,
		Config:                 cfg,
		Theme:                  ptrTheme(theme.ResolveByName(profile.Theme)),
		DescribeService:        describeService,
		KindRegistry:           registry,
		ResourceServices:       resourceServices,
		HomeKind:               homeKind,
		HomeCh:                 homeCh,
		HomeDone:               homeDone,
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

// ptrTheme adapts theme.ResolveByName's value return into the pointer
// shape tui.ModelDeps expects (nil-able, so tests can pass nil for
// "default").
func ptrTheme(t theme.Theme) *theme.Theme { return &t }
