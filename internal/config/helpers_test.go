// SPDX-License-Identifier: Apache-2.0

package config_test

import "os"

// writeFileBytes is a thin wrapper kept in a helper file so the main
// test file reads cleanly with body literals.
func writeFileBytes(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}
