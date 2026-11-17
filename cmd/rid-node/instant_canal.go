//go:build canal

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

// The canal image does not link the instant APIs; configuration already rejects instant
// services, so these only answer the commands.

const notInCanal = "no está disponible en modo canal: las APIs instantáneas vienen en la imagen completa (install.sh modo completo)"

func instantRuntime(context.Context, config.Service, *slog.Logger) (server.Route, agent.ServiceRuntime, func(), error) {
	return server.Route{}, agent.ServiceRuntime{}, nil, errors.New("las APIs instantáneas " + notInCanal)
}

func validateInstant(_ context.Context, s config.Service, out io.Writer) bool {
	fmt.Fprintf(out, "✘ servicio %q: las APIs instantáneas %s\n", s.Name, notInCanal)
	return false
}

func doctorInstant(_ context.Context, s config.Service, c *checker) {
	c.fail("servicio %s: las APIs instantáneas %s", s.Name, notInCanal)
}

func instantCommand(name string, _ []string, _ string, out io.Writer) int {
	fmt.Fprintf(out, "%s %s\n", name, notInCanal)
	return 2
}
