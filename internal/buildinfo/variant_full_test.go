//go:build !canal

package buildinfo

import "testing"

func TestVariant(t *testing.T) {
	if Variant() != "completo" || !InstantSupported() {
		t.Fatalf("default build: Variant()=%q InstantSupported()=%v", Variant(), InstantSupported())
	}
}
