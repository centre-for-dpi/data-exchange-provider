package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, yaml string) (*Node, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func mustFail(t *testing.T, yaml string, wantInMessage ...string) {
	t.Helper()
	_, err := load(t, yaml)
	if err == nil {
		t.Fatalf("expected an error for:\n%s", yaml)
	}
	for _, w := range wantInMessage {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not mention %q", err, w)
		}
	}
}

const validProxy = `
portal: https://portal.red.example
services:
  - name: certificados
    path: /certificados
    type: proxy
    upstream: https://api.interna.entidad/certificados/v1
    credential: { header: X-Api-Key, file: /run/secrets/certificados_credential }
`

func TestLoadValidProxy(t *testing.T) {
	n, err := load(t, validProxy)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Services) != 1 {
		t.Fatalf("services: %d", len(n.Services))
	}
	p := n.Services[0].Proxy
	if p.MaxRequestBytes != 1<<20 || p.ConnectTimeout != 5*time.Second || p.ResponseTimeout != 30*time.Second {
		t.Fatalf("defaults not applied: %+v", p)
	}
	if p.Credential == nil || p.Credential.Header != "X-Api-Key" {
		t.Fatalf("credential: %+v", p.Credential)
	}
}

func TestLoadNoServicesIsValid(t *testing.T) {
	if _, err := load(t, "portal: https://portal.red.example\n"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnknownField(t *testing.T) {
	mustFail(t, validProxy+"    colour: blue\n", "colour")
}

func TestRejectBadNameAndPath(t *testing.T) {
	mustFail(t, strings.Replace(validProxy, "name: certificados", "name: Salud!", 1), `"Salud!"`, "nombre")
	mustFail(t, strings.Replace(validProxy, "path: /certificados", "path: certificados", 1), "ruta")
	mustFail(t, strings.Replace(validProxy, "path: /certificados", "path: /"+strings.Repeat("a", 200), 1), "ruta")
}

func TestRejectDuplicateNameOrPath(t *testing.T) {
	two := validProxy + `
  - name: certificados
    path: /otra
    type: proxy
    upstream: https://api.interna.entidad/otra
`
	mustFail(t, two, "certificados", "repetido")
	two = validProxy + `
  - name: otro
    path: /certificados
    type: proxy
    upstream: https://api.interna.entidad/otra
`
	mustFail(t, two, "/certificados", "repetida")
}

func TestRejectOverlappingPaths(t *testing.T) {
	two := validProxy + `
  - name: otro
    path: /certificados/v2
    type: proxy
    upstream: https://api.interna.entidad/otra
`
	mustFail(t, two, "/certificados/v2", "/certificados")
}

func TestPrefixWithoutSegmentBoundaryIsNotOverlap(t *testing.T) {
	two := validProxy + `
  - name: otro
    path: /certificadosx
    type: proxy
    upstream: https://api.interna.entidad/otra
`
	if _, err := load(t, two); err != nil {
		t.Fatal(err)
	}
}

func TestRejectPlainHTTPWithoutOptIn(t *testing.T) {
	plain := strings.Replace(validProxy, "https://api.interna.entidad", "http://api.interna.entidad", 1)
	mustFail(t, plain, "allowPlainHttp")
	if _, err := load(t, plain+"    allowPlainHttp: true\n"); err != nil {
		t.Fatalf("opt-in should allow http: %v", err)
	}
}

func TestRejectTwoCredentialForms(t *testing.T) {
	both := strings.Replace(validProxy,
		"credential: { header: X-Api-Key, file: /run/secrets/certificados_credential }",
		"credential: { header: X-Api-Key, file: /run/secrets/x, basic: { user: u, passwordFile: /run/secrets/p } }", 1)
	mustFail(t, both, "credencial")
}

func TestRejectHalfCredential(t *testing.T) {
	half := strings.Replace(validProxy,
		"credential: { header: X-Api-Key, file: /run/secrets/certificados_credential }",
		"credential: { header: X-Api-Key }", 1)
	mustFail(t, half, "credencial")
}

func TestRejectInsecurePortal(t *testing.T) {
	mustFail(t, strings.Replace(validProxy, "https://portal.red.example", "http://portal.red.example", 1), "portal")
}

func TestDevelopmentPortalOverHTTP(t *testing.T) {
	for _, host := range []string{"localhost:4000", "host.docker.internal:4000", "api:4000"} {
		if _, err := load(t, strings.Replace(validProxy, "https://portal.red.example", "http://"+host, 1)); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
	}
}

func TestReadSecretTrimsNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(path, []byte("s3cr3t\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecret(path)
	if err != nil || got != "s3cr3t" {
		t.Fatalf("ReadSecret = %q, %v", got, err)
	}
}

func TestReadSecretEmptyIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(path); err == nil || !strings.Contains(err.Error(), "vacío") {
		t.Fatalf("expected an empty-secret error, got %v", err)
	}
}

// Final review: "/" was accepted but never routed.
func TestRejectRootServicePath(t *testing.T) {
	mustFail(t, strings.Replace(validProxy, "path: /certificados", "path: /", 1), "ruta")
}

// Final review: credentials in the upstream URL were dropped silently and printed by doctor.
func TestRejectUserinfoInUpstream(t *testing.T) {
	mustFail(t, strings.Replace(validProxy, "https://api.interna.entidad", "https://rid:s3cr3t@api.interna.entidad", 1), "credential.basic")
}

// K2: one file per service under services/, so the installer never edits YAML by hand.
func TestServicesDirectory(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "services"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "node.yaml"), []byte("portal: https://portal.red.example\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services", "b.yaml"), []byte("name: bbb\npath: /b\ntype: proxy\nupstream: https://api.interna/b\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services", "a.yaml"), []byte("name: aaa\npath: /a\ntype: proxy\nupstream: https://api.interna/a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services", "a.openapi.yaml"), []byte("openapi: 3.1.0\n"), 0o644) // not a service
	n, err := Load(filepath.Join(dir, "node.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Services) != 2 || n.Services[0].Name != "aaa" || n.Services[1].Name != "bbb" || n.Services[0].Proxy.MaxRequestBytes != DefaultMaxRequestBytes {
		t.Fatalf("services: %+v", n.Services)
	}
}

func TestServicesDirectoryErrorNamesFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "services"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "node.yaml"), []byte("portal: https://portal.red.example\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services", "roto.yaml"), []byte("name: x\ncolour: blue\n"), 0o644)
	if _, err := Load(filepath.Join(dir, "node.yaml")); err == nil || !strings.Contains(err.Error(), "roto.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestServicesDirectoryDuplicatesNodeYaml(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "services"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "node.yaml"), []byte(validProxy), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "services", "c.yaml"), []byte("name: certificados\npath: /otra\ntype: proxy\nupstream: https://api.interna/x\n"), 0o644)
	if _, err := Load(filepath.Join(dir, "node.yaml")); err == nil || !strings.Contains(err.Error(), "repetido") {
		t.Fatalf("got %v", err)
	}
}
