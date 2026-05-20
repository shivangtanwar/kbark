// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

// DefaultResyncInterval is the resync window applied to every kbark
// informer. Resync triggers a synthetic Update on every cached object
// so a slow consumer eventually re-reads stale state.
const DefaultResyncInterval = 30 * time.Second

// NewFactory builds a shared informer factory scoped to a single
// namespace. Pass an empty namespace to watch across all namespaces.
//
// The returned factory is the seam other listers (pods, deployments,
// services, …) hang off; one factory per namespace keeps cache
// invalidation simple when the user switches contexts.
func NewFactory(client kubernetes.Interface, namespace string, resync time.Duration) informers.SharedInformerFactory {
	opts := []informers.SharedInformerOption{}
	if namespace != "" {
		opts = append(opts, informers.WithNamespace(namespace))
	}
	return informers.NewSharedInformerFactoryWithOptions(client, resync, opts...)
}
