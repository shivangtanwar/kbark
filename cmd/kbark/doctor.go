// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shivangtanwar/kbark/internal/config"
	"github.com/shivangtanwar/kbark/internal/doctor"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check kbark's prerequisites (kubeconfig, apiserver, AI providers)",
	Long: `kbark doctor reports the state of every prerequisite kbark needs:
the kubeconfig file, the apiserver, and the configured AI providers
(Anthropic, OpenAI, Ollama).

Exit code is non-zero only if the kubeconfig or apiserver row is red;
AI provider failures are reported but do not fail the command.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		results := doctor.Run(ctx, kubeFlags, buildDoctorOptions())
		renderResults(os.Stdout, results)
		if doctor.ClusterFatal(results) {
			os.Exit(1)
		}
		return nil
	},
}

// buildDoctorOptions resolves the config + profile state for the
// doctor command. Failures (missing UserConfigDir, unknown profile)
// are surfaced via doctor.Options.ProfileErr rather than aborting
// the command — the doctor's job is to TELL the user what's broken,
// not exit before showing the kube state.
func buildDoctorOptions() doctor.Options {
	opts := doctor.Options{}
	cfgPath, _ := config.DefaultPath()
	opts.ConfigPath = cfgPath

	cfg, err := config.Load(cfgPath)
	if err != nil {
		opts.ProfileErr = err
		return opts
	}
	// Distinguish "file existed and loaded" from "fell back to defaults".
	if cfgPath != "" {
		if _, statErr := os.Stat(cfgPath); statErr == nil {
			opts.ConfigLoaded = true
		}
	}

	profile, err := cfg.Resolve(profileFlag)
	if err != nil {
		opts.ProfileErr = err
		return opts
	}
	opts.Profile = profileFlag
	if opts.Profile == "" {
		opts.Profile = cfg.DefaultProfile
	}
	opts.Provider = profile.Provider
	opts.Model = profile.Model
	opts.TokenBudget = profile.TokenBudget
	return opts
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func renderResults(w io.Writer, results []doctor.Result) {
	useColor := false
	if f, ok := w.(*os.File); ok {
		useColor = term.IsTerminal(int(f.Fd()))
	}
	for _, r := range results {
		fmt.Fprintf(
			w, "%-12s %s  %s\n",
			r.Name+":",
			colorize(r.Status, useColor),
			r.Detail,
		)
	}
}

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

func colorize(s doctor.Status, useColor bool) string {
	label := s.Label()
	if !useColor {
		return label
	}
	var color string
	switch s {
	case doctor.Green:
		color = ansiGreen
	case doctor.Yellow:
		color = ansiYellow
	case doctor.Red:
		color = ansiRed
	default:
		color = ansiReset
	}
	return color + label + ansiReset
}
