//go:build canal

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCanalSaysNotAvailable(t *testing.T) {
	for _, cmd := range [][]string{{"catalog", "--service-file", "x.yaml"}, {"scaffold"}, {"openapi", "afiliacion"}} {
		var out bytes.Buffer
		if code := run(cmd, &out); code != 2 || !strings.Contains(out.String(), "modo canal") {
			t.Fatalf("%v: code=%d %s", cmd, code, out.String())
		}
	}
}
