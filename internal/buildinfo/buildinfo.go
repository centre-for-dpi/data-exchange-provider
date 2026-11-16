// Package buildinfo holds the kit's version, the node contract it speaks and the build variant.
package buildinfo

// Version is the kit version reported to the portal (kitVersion). Set at build time with
// -ldflags "-X github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo.Version=...".
var Version = "0.1.0-dev"

// ContractVersion is the version of the portal's node contract this kit implements.
const ContractVersion = "1"
