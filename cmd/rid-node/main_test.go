//go:build !canal

package main

import (
	"bytes"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version"}, &out); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	want := "rid-node 0.1.0-dev (completo), contrato de nodos v1\n"
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
