// SPDX-License-Identifier: Apache-2.0

package theme

// HighContrast is a placeholder for the M9 polish pass.
// Returning the default theme keeps callers compiling without forcing them
// to special-case nil. Replaced with the real high-contrast palette later.
func HighContrast() Theme {
	return Default()
}
