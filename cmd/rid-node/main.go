// Command rid-node is the provider node of the data exchange network: it enrolls with the
// portal, keeps a WireGuard tunnel to the network's hub and serves the entity's services only
// to the gateway.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
)

const usage = `uso: rid-node <comando>

comandos:
  run              arranca el nodo (punto de entrada del contenedor)
  status [--json]  estado del nodo
  doctor           revisa conectividad y configuración
  validate         valida la configuración y los secretos de los servicios
  identity         nombre del nodo si ya está enrolado (salida 3 si no tiene identidad)
  version          versión del kit

variables: RID_CONFIG (/etc/rid-node/node.yaml), RID_STATE_DIR (/var/lib/rid-node),
           RID_TOKEN_FILE (/run/secrets/enrollment_token), RID_HEARTBEAT_SECONDS (solo desarrollo)
`

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// heartbeatOverride reads RID_HEARTBEAT_SECONDS (development): never below 10 s, so a typo
// cannot hammer the portal; 0 or invalid means "follow the portal's interval".
func heartbeatOverride(s string) time.Duration {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(max(n, 10)) * time.Second
}

func run(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return 2
	}
	configPath := env("RID_CONFIG", "/etc/rid-node/node.yaml")
	stateDir := env("RID_STATE_DIR", "/var/lib/rid-node")
	tokenFile := env("RID_TOKEN_FILE", "/run/secrets/enrollment_token")
	switch args[0] {
	case "version":
		fmt.Fprintf(out, "rid-node %s (%s), contrato de nodos v%s\n", buildinfo.Version, buildinfo.Variant(), buildinfo.ContractVersion)
		return 0
	case "run":
		log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runNode(ctx, runOptions{ConfigPath: configPath, StateDir: stateDir, TokenFile: tokenFile,
			HeartbeatEvery: heartbeatOverride(os.Getenv("RID_HEARTBEAT_SECONDS")), Log: log}); err != nil {
			log.Error("fatal", "error", err.Error())
			return 1
		}
		return 0
	case "identity":
		return identity(stateDir, out)
	case "import-state":
		if len(args) < 2 {
			fmt.Fprint(out, usage)
			return 2
		}
		return importState(args[1], stateDir, out)
	case "validate":
		return validate(configPath, out)
	case "status":
		return status(stateDir, len(args) > 1 && args[1] == "--json", out, time.Now)
	case "doctor":
		return doctor(context.Background(), doctorOptions{ConfigPath: configPath, StateDir: stateDir, TokenFile: tokenFile,
			PortalHTTP: contract.NewPortalHTTPClient(nil), Now: time.Now}, out)
	default:
		fmt.Fprint(out, usage)
		return 2
	}
}
