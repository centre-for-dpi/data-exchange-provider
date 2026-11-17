//go:build !canal

package instant

import (
	"strings"
	"testing"
)

func TestScaffold(t *testing.T) {
	r, skipped, err := Scaffold(upperCatalog(), ScaffoldOptions{Source: "salud.v_afiliados", Key: "numero_documento", Subject: "CC"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "afiliados" || r.Entity != "Afiliados" || r.Source != "salud.v_afiliados" || r.Key.Column != "numero_documento" ||
		r.DataSubject.DocumentType != "CC" || r.Many {
		t.Fatalf("%+v", r)
	}
	var names []string
	for _, c := range r.Columns {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "numero_documento,nombres,activo,fecha_afiliacion" {
		t.Fatalf("columns: %v (lower case, in catalog order, exposable only)", names)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "foto") || !strings.Contains(skipped[0], "BLOB") {
		t.Fatalf("skipped: %v", skipped)
	}
	r, _, err = Scaffold(upperCatalog(), ScaffoldOptions{Source: "SALUD.V_AFILIADOS", Key: "NUMERO_DOCUMENTO", Resource: "personas", Entity: "Persona", Many: true})
	if err != nil || r.Name != "personas" || r.Entity != "Persona" || !r.Many || r.DataSubject != nil || r.Key.Column != "numero_documento" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []ScaffoldOptions{
		{Source: "salud.no_existe", Key: "id"},
		{Source: "salud.v_afiliados", Key: "foto"},
		{Source: "salud.v_afiliados", Key: "no_existe"},
		{Source: "salud.v_afiliados", Key: "numero_documento", Resource: "Mal Nombre"},
	} {
		if _, _, err := Scaffold(upperCatalog(), bad); err == nil {
			t.Fatalf("%+v must fail", bad)
		}
	}
}

// Columns that differ only in case (PostgreSQL "Nombre" and "nombre") would collide once lower
// cased: both are left out, with a note.
func TestScaffoldSkipsCaseCollisions(t *testing.T) {
	cat := upperCatalog()
	cat[0].Columns = append(cat[0].Columns, Column{Name: "Nombres", Native: "varchar", Kind: KindString})
	r, skipped, err := Scaffold(cat, ScaffoldOptions{Source: "salud.v_afiliados", Key: "numero_documento"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Columns {
		if c.Name == "nombres" {
			t.Fatalf("a colliding column was kept: %+v", r.Columns)
		}
	}
	if !strings.Contains(strings.Join(skipped, "|"), "Nombres") || !strings.Contains(strings.Join(skipped, "|"), "NOMBRES") {
		t.Fatalf("skipped: %v", skipped)
	}
}
