// Command den-node is the provider node of the data exchange network: it enrolls with the
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
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

const usage = `usage: den-node <command>

commands:
  run              starts the node (the container's entry point)
  status [--json]  the node's status
  doctor           checks connectivity and configuration
  validate         validates the configuration and the services' secrets
  identity         the node's name if it is enrolled (exit 3 if it has no identity)
  version          the kit's version

Instant APIs (full mode):
  catalog --service-file <file>        views and tables the database user can read
  scaffold --service-file <file> --source schema.view --key column
           [--resource name] [--entity Name] [--subject TYPE] [--many]
                                       adds a resource with every column that can be exposed
  openapi <service>                    prints the generated OpenAPI

variables: DEN_CONFIG (/etc/den-node/node.yaml), DEN_STATE_DIR (/var/lib/den-node),
           DEN_TOKEN_FILE (/run/secrets/enrollment_token), DEN_HEARTBEAT_SECONDS (development only)
`

func main() {
	if w := i18n.FromEnv(); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
	os.Exit(run(os.Args[1:], os.Stdout))
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// heartbeatOverride reads DEN_HEARTBEAT_SECONDS (development): never below 10 s, so a typo
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
		fmt.Fprint(out, i18n.T(usage))
		return 2
	}
	configPath := env("DEN_CONFIG", "/etc/den-node/node.yaml")
	stateDir := env("DEN_STATE_DIR", "/var/lib/den-node")
	tokenFile := env("DEN_TOKEN_FILE", "/run/secrets/enrollment_token")
	switch args[0] {
	case "version":
		i18n.Fprintf(out, "den-node %s (%s), node contract v%s\n", buildinfo.Version, buildinfo.Variant(), buildinfo.ContractVersion)
		return 0
	case "run":
		log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runNode(ctx, runOptions{ConfigPath: configPath, StateDir: stateDir, TokenFile: tokenFile,
			HeartbeatEvery: heartbeatOverride(os.Getenv("DEN_HEARTBEAT_SECONDS")), Log: log}); err != nil {
			log.Error("fatal", "error", err.Error())
			return 1
		}
		return 0
	case "identity":
		return identity(stateDir, out)
	case "import-state":
		if len(args) < 2 {
			fmt.Fprint(out, i18n.T(usage))
			return 2
		}
		return importState(args[1], stateDir, out)
	case "validate":
		return validate(configPath, out)
	case "catalog", "scaffold", "openapi":
		return instantCommand(args[0], args[1:], configPath, out)
	case "status":
		return status(stateDir, len(args) > 1 && args[1] == "--json", out, time.Now)
	case "doctor":
		return doctor(context.Background(), doctorOptions{ConfigPath: configPath, StateDir: stateDir, TokenFile: tokenFile,
			PortalHTTP: contract.NewPortalHTTPClient(nil), Now: time.Now}, out)
	default:
		fmt.Fprint(out, i18n.T(usage))
		return 2
	}
}
