// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"fmt"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

// AllowedVerbs is the read-only set of API verbs kbark may use in v1.
// Enforcement is wired alongside the AI tool-call layer in a later milestone;
// for now this constant exists to anchor the contract.
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
