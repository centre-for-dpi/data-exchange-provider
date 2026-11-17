package i18n

import (
	"errors"
	"strings"
	"testing"
)

func withCatalog(t *testing.T, cat map[string]string) {
	t.Helper()
	old := es
	es = cat
	t.Cleanup(func() { es = old; _ = Set("en") })
}

func TestEnglishByDefault(t *testing.T) {
	withCatalog(t, map[string]string{"service %q not found": "no se encontró el servicio %q"})
	if Current() != EN {
		t.Fatalf("default language %q", Current())
	}
	if got := Sprintf("service %q not found", "x"); got != `service "x" not found` {
		t.Fatalf("%q", got)
	}
}

func TestSpanish(t *testing.T) {
	withCatalog(t, map[string]string{"service %q not found": "no se encontró el servicio %q", "cannot read: %w": "no se pudo leer: %w"})
	if err := Set("es"); err != nil {
		t.Fatal(err)
	}
	if got := Sprintf("service %q not found", "x"); got != `no se encontró el servicio "x"` {
		t.Fatalf("%q", got)
	}
	base := errors.New("boom")
	err := Errorf("cannot read: %w", base)
	if err.Error() != "no se pudo leer: boom" || !errors.Is(err, base) {
		t.Fatalf("%v", err)
	}
	// A message without a translation falls back to English instead of disappearing.
	if got := T("untranslated"); got != "untranslated" {
		t.Fatalf("%q", got)
	}
}

func TestSetRejectsUnknown(t *testing.T) {
	if err := Set("fr"); err == nil {
		t.Fatal("fr accepted")
	}
	if Current() != EN {
		t.Fatal("a rejected language changed the current one")
	}
}

func TestFromEnv(t *testing.T) {
	t.Cleanup(func() { _ = Set("en") })
	for _, c := range []struct {
		env  string
		want Lang
		warn bool
	}{{"", EN, false}, {"es", ES, false}, {"ES", ES, false}, {"es_CO.UTF-8", ES, false}, {"en", EN, false}, {"fr", EN, true}} {
		t.Setenv("DEN_LANG", c.env)
		warn := FromEnv()
		if Current() != c.want || (warn != "") != c.warn {
			t.Fatalf("DEN_LANG=%q → %q, warning %q", c.env, Current(), warn)
		}
	}
}

// The catalog is complete and consistent: every message the code passes to this package has
// a Spanish translation with the same verbs, and no translation is left without its message.
func TestCatalogMatchesCode(t *testing.T) {
	used, problems := scanMessages(t, "../..")
	for _, p := range problems {
		t.Error(p)
	}
	for msg, where := range used {
		tr, ok := es[msg]
		if !ok {
			t.Errorf("%s: no Spanish translation for %q", where, msg)
			continue
		}
		if a, b := verbs(msg), verbs(tr); strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("%q: verbs %v, Spanish %q has %v", msg, a, tr, b)
		}
	}
	for msg := range es {
		if _, ok := used[msg]; !ok {
			t.Errorf("translation of %q is not used by the code", msg)
		}
	}
}
