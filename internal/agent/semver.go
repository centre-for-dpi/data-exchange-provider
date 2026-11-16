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
	}
	return comparePreRelease(strings.Split(ap, "."), strings.Split(bp, "."))
}

// comparePreRelease follows semver 2.0: numeric identifiers compare numerically and sort before
// alphanumeric ones; a shorter list of equal identifiers sorts first.
func comparePreRelease(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		an, aErr := strconv.Atoi(a[i])
		bn, bErr := strconv.Atoi(b[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		case a[i] != b[i]:
			return sign(strings.Compare(a[i], b[i]))
		}
	}
	return sign(len(a) - len(b))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
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
