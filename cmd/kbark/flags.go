// SPDX-License-Identifier: Apache-2.0

package main

import "k8s.io/cli-runtime/pkg/genericclioptions"

// kubeFlags holds the kubectl-compatible persistent flags
// (--kubeconfig, --context, --namespace, --cluster, --user, ...) read by
// every kbark subcommand that talks to a cluster.
var kubeFlags *genericclioptions.ConfigFlags

func init() {
	kubeFlags = genericclioptions.NewConfigFlags(true)
	kubeFlags.AddFlags(rootCmd.PersistentFlags())
}
