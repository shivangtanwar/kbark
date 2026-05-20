// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"log"
	"os"
)

// debugLogger writes detailed loop / dispatch / event traces to a file
// when the KBARK_DEBUG env var is set. The file is /tmp/kbark-debug.log
// by default — a known location so the user can `tail -f` it without
// interfering with the TUI's alt-screen output.
var debugLogger *log.Logger

func init() {
	if os.Getenv("KBARK_DEBUG") == "" {
		return
	}
	path := os.Getenv("KBARK_DEBUG_PATH")
	if path == "" {
		path = "/tmp/kbark-debug.log"
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	debugLogger = log.New(f, "[kbark] ", log.LstdFlags|log.Lmicroseconds)
}

func debugf(format string, args ...any) {
	if debugLogger != nil {
		debugLogger.Printf(format, args...)
	}
}
