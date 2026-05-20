// SPDX-License-Identifier: Apache-2.0

// Package kube wraps the Kubernetes client machinery used by kbark:
// kubeconfig loading, clientset construction, and the read-only verb
// allowlist that gates every API call.
package kube

import (
	"fmt"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
)

// RESTConfig resolves the persistent kubeconfig flags into a rest.Config.
// Honours $KUBECONFIG, --kubeconfig, --context, --namespace, --cluster
// and --user, exactly like kubectl.
func RESTConfig(flags *genericclioptions.ConfigFlags) (*rest.Config, error) {
	cfg, err := flags.ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}
