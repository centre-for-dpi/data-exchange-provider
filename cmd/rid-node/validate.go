package main

import (
	"context"
	"fmt"
	"io"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/proxy"
)

// validate loads the configuration and builds every service (which reads its secrets), exactly
// as `run` would, without touching the network or the node's state.
func validate(configPath string, out io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	for _, w := range cfg.Warnings() {
		fmt.Fprintf(out, "⚠ %s\n", w)
	}
	for _, s := range cfg.Services {
		if s.Type == "instant" {
			if !validateInstant(context.Background(), s, out) {
				return 1
			}
			continue
		}
		if _, err := proxy.New(s.Path, s.Proxy); err != nil {
			fmt.Fprintf(out, "✘ servicio %q: %v\n", s.Name, err)
			return 1
		}
	}
	fmt.Fprintf(out, "✔ configuración válida: %d servicios\n", len(cfg.Services))
	return 0
}
