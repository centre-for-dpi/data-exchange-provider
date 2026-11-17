//go:build !canal

package main

import (
	"bytes"
	"testing"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version"}, &out); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	want := "rid-node 0.1.0-dev (completo), node contract v1\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"nope"}, &out); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}

func TestAssertionAudience(t *testing.T) {
	cases := []struct {
		configured, gateway, mode, want string
		fails                           bool
	}{
		{"", "rid-gateway.salud", "verify", "salud", false},
		{"", "gw-red.salud", "verify", "salud", false}, // the prefix is the portal's setting
		{"otra", "rid-gateway.salud", "require", "otra", false},
		{"", "gateway-propio", "verify", "", false}, // unknown: verification off, with a warning
		{"", "gateway-propio", "require", "", true}, // unknown and required: the node does not start
	}
	for _, c := range cases {
		got, err := assertionAudience(config.GatewayAssertion{Mode: c.mode, Audience: c.configured}, c.gateway)
		if got != c.want || (err != nil) != c.fails {
			t.Fatalf("%+v: %q %v", c, got, err)
		}
	}
}
