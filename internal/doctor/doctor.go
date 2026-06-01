// SPDX-License-Identifier: Apache-2.0

// Package doctor implements the kbark doctor health-check pipeline.
// Each check returns a Result; the CLI renders one colored row per result
// and sets the process exit code from ClusterFatal.
package doctor

import (
	"context"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// Status is the outcome of a single check.
type Status int

const (
	// Green: configured and verified working.
	Green Status = iota
	// Yellow: configured but unverified — transient network/server-side condition.
	Yellow
	// Red: unconfigured or hard failure (missing env, auth rejected, unreachable).
	Red
)

func (s Status) Label() string {
	switch s {
	case Green:
		return "OK"
	case Yellow:
		return "WARN"
	case Red:
		return "FAIL"
	default:
		return "??"
	}
}

// Result is one health-check outcome.
type Result struct {
	Name   string
	Status Status
	Detail string
}

// Options carries profile-resolution state into Run so the doctor
// output can show which config file was loaded, which profile is
// active, and which provider row corresponds to the active one.
// All fields are optional; zero-value behaves like the pre-M8.2
// doctor that knew nothing about config.
type Options struct {
	// ConfigPath is the resolved on-disk config file path (whether
	// or not it exists). Empty if UserConfigDir() failed.
	ConfigPath string
	// ConfigLoaded reports whether the config file actually existed
	// and parsed successfully. False means built-in defaults are in
	// use — not an error condition.
	ConfigLoaded bool
	// Profile is the resolved active profile name.
	Profile string
	// Provider is the AI provider name dictated by the active
	// profile ("anthropic" / "openai" / "ollama").
	Provider string
	// Model is the provider-specific model identifier.
	Model string
	// TokenBudget is the per-session estimated-tokens cap from the
	// active profile (0 = unbounded). Surfaced in the profile row
	// when non-zero so the user can see the limit at a glance.
	TokenBudget int
	// ProfileErr, when non-nil, indicates the --profile flag pointed
	// at an unknown name or the config file was malformed. The
	// doctor surfaces this as a RED "profile" row but continues
	// with the other checks so the user can still see kubeconfig
	// state.
	ProfileErr error
}

// Run executes every check in a fixed order: config, profile,
// kubeconfig, apiserver, then each AI provider. Provider checks
// always run so users see their full posture, even when the cluster
// checks or profile resolution failed.
func Run(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, opts Options) []Result {
	results := make([]Result, 0, 8)
	results = append(results, checkConfig(opts)...)

	kubeRes, restCfg := checkKubeconfig(kubeFlags)
	results = append(results, kubeRes)

	if kubeRes.Status == Green && restCfg != nil {
		results = append(results, checkAPIServer(ctx, restCfg))
	} else {
		results = append(results, Result{
			Name:   "apiserver",
			Status: Red,
			Detail: "skipped (kubeconfig failed)",
		})
	}

	results = append(
		results,
		markActive(checkAnthropic(ctx), opts),
		markActive(checkOpenAI(ctx), opts),
		markActive(checkOllama(ctx), opts),
	)
	return results
}

// ClusterFatal reports whether the cluster-side prerequisites are red.
// The CLI uses this to set a non-zero exit code; AI-provider rows never
// fail the command.
func ClusterFatal(results []Result) bool {
	for _, r := range results {
		if (r.Name == "kubeconfig" || r.Name == "apiserver") && r.Status == Red {
			return true
		}
	}
	return false
}
