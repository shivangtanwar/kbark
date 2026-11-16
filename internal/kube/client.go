// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"fmt"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

// AllowedVerbs is the read-only set of API verbs the terminal UI and
// the AI tool layer may use. The autofix layer's writes do not go
// through this package: they are confined to internal/remediate, whose
// five typed actions need update on workloads, patch on nodes and
// delete on pods (see deploy/rbac.yaml).
var AllowedVerbs = []string{"get", "list", "watch", "getlog"}

// NewClientset builds a typed Kubernetes clientset from the persistent flags.
func NewClientset(flags *genericclioptions.ConfigFlags) (*kubernetes.Clientset, error) {
	cfg, err := RESTConfig(flags)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return clientset, nil
}
