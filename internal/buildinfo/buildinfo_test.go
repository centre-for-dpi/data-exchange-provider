package buildinfo

import (
	"regexp"
	"testing"
)

// The contract's kitVersion pattern (node contract v1, enroll and heartbeat bodies).
var kitVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)

func TestVersionMatchesContractPattern(t *testing.T) {
	if !kitVersionPattern.MatchString(Version) {
		t.Fatalf("Version %q does not match the contract's kitVersion pattern", Version)
	}
}

func TestContractVersion(t *testing.T) {
	if ContractVersion != "1" {
		t.Fatalf("ContractVersion = %q, want 1", ContractVersion)
	}
}
