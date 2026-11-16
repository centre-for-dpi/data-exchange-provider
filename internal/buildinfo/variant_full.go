//go:build !canal

package buildinfo

// Variant is the image variant: "completo" serves proxy and instant services.
func Variant() string { return "completo" }

// InstantSupported reports whether this build includes the instant APIs module.
func InstantSupported() bool { return true }
