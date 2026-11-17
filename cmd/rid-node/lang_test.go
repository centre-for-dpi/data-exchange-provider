package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// The CLI speaks English by default and Spanish with RID_LANG=es, errors from the packages
// below it included.
func TestCLILanguage(t *testing.T) {
	t.Cleanup(func() { _ = i18n.Set("en") })
	missing := filepath.Join(t.TempDir(), "node.yaml")
	for _, c := range []struct{ lang, want, not string }{
		{"", "✘ cannot read the configuration", "no se pudo leer"},
		{"es", "✘ no se pudo leer la configuración", "cannot read"},
	} {
		t.Setenv("RID_LANG", c.lang)
		if w := i18n.FromEnv(); w != "" {
			t.Fatal(w)
		}
		var out bytes.Buffer
		if code := validate(missing, &out); code != 1 || !strings.Contains(out.String(), c.want) || strings.Contains(out.String(), c.not) {
			t.Fatalf("RID_LANG=%q: code=%d %s", c.lang, code, out.String())
		}
		out.Reset()
		run([]string{"nope"}, &out)
		if usage := map[string]string{"": "usage: rid-node", "es": "uso: rid-node"}[c.lang]; !strings.HasPrefix(out.String(), usage) {
			t.Fatalf("RID_LANG=%q: usage %s", c.lang, out.String())
		}
	}
}
