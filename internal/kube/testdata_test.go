// SPDX-License-Identifier: Apache-2.0

package kube_test

import "time"

const (
	testNamespace = "default"
	// Generous timeout: informer initial sync against the fake clientset
	// is usually microseconds, but CI runners can be slow under load.
	snapshotTimeout = 500 * time.Millisecond
)
