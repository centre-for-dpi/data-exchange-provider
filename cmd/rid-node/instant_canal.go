//go:build canal

package main

import (
	"context"
	"io"
	"log/slog"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

// The canal image does not link the instant APIs; configuration already rejects instant
// services, so these only answer the commands.

const notInCanal = "not available in canal mode: the instant APIs come in the full image (install.sh mode completo)"

func instantRuntime(context.Context, config.Service, *slog.Logger) (server.Route, agent.ServiceRuntime, func(), error) {
	return server.Route{}, agent.ServiceRuntime{}, nil, i18n.New("the instant APIs are " + notInCanal)
}

func validateInstant(_ context.Context, s config.Service, out io.Writer) bool {
	i18n.Fprintf(out, "✘ service %q: the instant APIs are "+notInCanal+"\n", s.Name)
	return false
}

func doctorInstant(_ context.Context, s config.Service, c *checker) {
	c.fail(i18n.Sprintf("service %s: the instant APIs are "+notInCanal, s.Name))
}

func instantCommand(name string, _ []string, _ string, out io.Writer) int {
	i18n.Fprintf(out, "%s is "+notInCanal+"\n", name)
	return 2
}
