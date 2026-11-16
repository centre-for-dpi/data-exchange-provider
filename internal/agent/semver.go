package agent

import (
	"strconv"
	"strings"
)

// compareSemver compares two versions like 1.2.3[-pre][+build]: -1, 0 or 1. A pre-release is
// lower than its release; build metadata is ignored.
func compareSemver(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)
	for i := 0; i < 3; i++ {
		if ac[i] != bc[i] {
			if ac[i] < bc[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	default:
		return 1
	}
}

func splitVersion(v string) ([3]int, string) {
	v, _, _ = strings.Cut(v, "+")
	core, pre, _ := strings.Cut(v, "-")
	var n [3]int
	for i, part := range strings.SplitN(core, ".", 3) {
		n[i], _ = strconv.Atoi(part)
	}
	return n, pre
}
