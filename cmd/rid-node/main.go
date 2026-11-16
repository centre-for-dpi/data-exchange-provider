// Command rid-node is the provider node of the data exchange network: it enrolls with the
// portal, keeps a WireGuard tunnel to the network's hub and serves the entity's services only
// to the gateway.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo"
)

const usage = `uso: rid-node <comando>

comandos:
  run       arranca el nodo (punto de entrada del contenedor)
  status    estado del nodo
  doctor    revisa conectividad y configuración
  version   versión del kit
`

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func run(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(out, "rid-node %s (%s), contrato de nodos v%s\n",
			buildinfo.Version, buildinfo.Variant(), buildinfo.ContractVersion)
		return 0
	case "run", "status", "doctor":
		fmt.Fprintf(out, "%s: no implementado todavía\n", args[0])
		return 1
	default:
		fmt.Fprint(out, usage)
		return 2
	}
}
