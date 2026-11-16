package state

import "runtime"

// isWindows: chmod is a no-op on Windows (development machines only; the kit runs on Ubuntu).
func isWindows() bool { return runtime.GOOS == "windows" }
