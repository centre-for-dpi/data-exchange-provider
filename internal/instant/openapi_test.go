//go:build !canal

package instant

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func goldenService() (config.Service, []Compiled) {
	a := afiliados(
		config.ColumnSpec{Name: "numero_documento", Description: "Número del documento"},
		config.ColumnSpec{Name: "nombres"},
		config.ColumnSpec{Name: "activo", As: "boolean"},
		config.ColumnSpec{Name: "fecha_afiliacion", As: "date"},
	)
	a.Description = "Afiliado por número de documento"
	a.Key.Description = "Número del documento de identidad"
	a.DataSubject = &config.SubjectSpec{DocumentType: "CC"}
	h := afiliados(config.ColumnSpec{Name: "numero_documento"}, config.ColumnSpec{Name: "fecha_afiliacion"})
	h.Name, h.Entity, h.Many, h.MaxRows = "historial", "Novedad", true, 50
	svc := service(a, h)
	svc.Instant.Description = "Consulta de afiliación"
	rs, err := Compile(svc, upperCatalog(), fakeDialect{})
	if err != nil {
		panic(err)
	}
	return svc, rs
}

func TestOpenAPIGolden(t *testing.T) {
	svc, rs := goldenService()
	got, err := OpenAPI(svc, rs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "afiliacion.openapi.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("OpenAPI differs from %s (run go test -run TestOpenAPIGolden -update, review and lint it):\n%s", path, got)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/afiliados/{numero_documento}"].(map[string]any)["get"].(map[string]any)
	if op["operationId"] != "getAfiliado" {
		t.Fatalf("operationId: %v", op["operationId"])
	}
	subject := op["x-rid-data-subject"].(map[string]any)
	if subject["in"] != "path" || subject["name"] != "numero_documento" || subject["documentType"] != "CC" {
		t.Fatalf("x-rid-data-subject: %v", subject)
	}
	many := doc["paths"].(map[string]any)["/historial/{numero_documento}"].(map[string]any)["get"].(map[string]any)
	if many["x-rid-data-subject"] != "none" {
		t.Fatalf("a resource without dataSubject declares none: %v", many["x-rid-data-subject"])
	}
	body := many["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if body["type"] != "array" || body["maxItems"] != float64(50) {
		t.Fatalf("many: %v", body)
	}
}

func TestOpenAPIDeterministic(t *testing.T) {
	svc, rs := goldenService()
	first, err := OpenAPI(svc, rs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		svc, rs := goldenService()
		again, _ := OpenAPI(svc, rs)
		if !bytes.Equal(first, again) {
			t.Fatal("the OpenAPI changes between runs")
		}
	}
}
