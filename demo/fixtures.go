// SPDX-License-Identifier: Apache-2.0

// Package demo carries the failure fixtures used by demo/demo.sh, so
// the console's demo lab applies exactly the same manifests.
package demo

import "embed"

// Fixtures holds fixtures/*.yaml.
//
//go:embed fixtures/*.yaml
var Fixtures embed.FS
