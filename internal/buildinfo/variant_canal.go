//go:build canal

package buildinfo

// Variant is the image variant: "canal" is the secure channel only, without instant APIs or
// database drivers.
func Variant() string { return "canal" }

// InstantSupported reports whether this build includes the instant APIs module.
func InstantSupported() bool { return false }
