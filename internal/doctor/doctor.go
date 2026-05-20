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

// Run executes every check in a fixed order: kubeconfig, apiserver, then each
// AI provider. Provider checks always run so users see their full posture,
// even when the cluster checks are red.
func Run(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags) []Result {
	results := make([]Result, 0, 5)

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

	results = append(results,
		checkAnthropic(ctx),
		checkOpenAI(ctx),
		checkOllama(ctx),
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
