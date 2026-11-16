//go:build canal

package buildinfo

import "testing"

func TestVariantCanal(t *testing.T) {
	if Variant() != "canal" || InstantSupported() {
		t.Fatalf("canal build: Variant()=%q InstantSupported()=%v", Variant(), InstantSupported())
	}
}
