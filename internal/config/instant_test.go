//go:build !canal

package config

import (
	"strings"
	"testing"
	"time"
)

const validInstant = `
portal: https://portal.red.example
services:
  - name: afiliacion
    path: /salud
    type: instant
    database:
      engine: postgres
      host: bd.interna
      port: 5432
      name: entidad
      user: rid_reader
      passwordFile: /run/secrets/afiliacion_db_password
    resources:
      - name: afiliados
        entity: Afiliado
        source: salud.v_afiliados
        key: { column: numero_documento }
        dataSubject: { documentType: CC }
        columns:
          - { name: numero_documento }
          - { name: activo, as: boolean }
`

func TestInstantValid(t *testing.T) {
	n, err := load(t, validInstant)
	if err != nil {
		t.Fatal(err)
	}
	s := n.Services[0].Instant
	if s.Database.Engine != "postgres" || len(s.Resources) != 1 || s.Resources[0].Columns[1].As != "boolean" {
		t.Fatalf("%+v", s)
	}
}

func TestInstantDefaults(t *testing.T) {
	n, err := load(t, validInstant)
	if err != nil {
		t.Fatal(err)
	}
	s := n.Services[0].Instant
	if s.Title != "afiliacion" || s.Version != "1.0.0" || s.Database.TLS != "require" || s.Database.MaxConnections != 4 ||
		s.Database.QueryTimeout != 5*time.Second || s.Resources[0].MaxRows != 100 {
		t.Fatalf("defaults: %+v db=%+v", s, s.Database)
	}
}

func TestInstantRejectsProxyFields(t *testing.T) {
	mustFail(t, strings.Replace(validInstant, "    type: instant\n", "    type: instant\n    upstream: https://x\n", 1), "upstream")
}

func TestProxyRejectsInstantFields(t *testing.T) {
	mustFail(t, validProxy+"    database: { engine: postgres }\n", "database")
}

func TestDatabaseRequiredFields(t *testing.T) {
	for _, field := range []string{"engine: postgres", "host: bd.interna", "name: entidad", "user: rid_reader", "passwordFile: /run/secrets/afiliacion_db_password"} {
		mustFail(t, strings.Replace(validInstant, field, "", 1), "base de datos")
	}
	mustFail(t, strings.Replace(validInstant, "engine: postgres", "engine: db2", 1), "db2")
	mustFail(t, strings.Replace(validInstant, "port: 5432", "port: 5432\n      tls: maybe", 1), "tls")
}

func TestOracleVerifyWithCAFileRejected(t *testing.T) {
	y := strings.Replace(validInstant, "engine: postgres", "engine: oracle", 1)
	y = strings.Replace(y, "port: 5432", "port: 1521\n      tls: verify\n      caFile: /etc/rid-node/ca.pem", 1)
	mustFail(t, y, "caFile")
}

func TestResourceValidation(t *testing.T) {
	cases := []struct{ name, from, to, want string }{
		{"bad resource name", "- name: afiliados", "- name: Afiliados!", "Afiliados!"},
		{"bad entity", "entity: Afiliado", "entity: afiliado", "entity"},
		{"bad source", "source: salud.v_afiliados", "source: v_afiliados", "source"},
		{"no key column", "key: { column: numero_documento }", "key: { pattern: x }", "key.column"},
		{"bad key pattern", "key: { column: numero_documento }", "key: { column: numero_documento, pattern: '[' }", "pattern"},
		{"bad as", "as: boolean", "as: blob", "blob"},
		{"empty document type", "dataSubject: { documentType: CC }", "dataSubject: { documentType: '' }", "documentType"},
		{"bad maxRows", "key: { column: numero_documento }", "key: { column: numero_documento }\n        maxRows: 5000", "maxRows"},
		{"duplicate column", "- { name: activo, as: boolean }", "- { name: NUMERO_documento }", "repetida"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(validInstant, c.from) {
				t.Fatalf("fixture lacks %q", c.from)
			}
			mustFail(t, strings.Replace(validInstant, c.from, c.to, 1), "recurso", c.want)
		})
	}
	two := strings.Replace(validInstant, "    resources:\n", "    resources:\n      - { name: afiliados, entity: Otro, source: a.b, key: { column: id } }\n", 1)
	mustFail(t, two, "afiliados", "repetido")
	two = strings.Replace(validInstant, "    resources:\n", "    resources:\n      - { name: otro, entity: Afiliado, source: a.b, key: { column: id } }\n", 1)
	mustFail(t, two, "Afiliado", "repetida")
}

func TestInstantWithoutResourcesIsValid(t *testing.T) {
	i := strings.Index(validInstant, "    resources:")
	if _, err := load(t, validInstant[:i]); err != nil {
		t.Fatalf("a service without resources yet (the installer's first step) must load: %v", err)
	}
	n, _ := load(t, validInstant[:i])
	if w := n.Warnings(); len(w) != 1 || !strings.Contains(w[0], "afiliacion") || !strings.Contains(w[0], "recursos") {
		t.Fatalf("warnings: %q", w)
	}
	n, _ = load(t, validInstant)
	if w := n.Warnings(); len(w) != 0 {
		t.Fatalf("no warnings expected: %q", w)
	}
}
