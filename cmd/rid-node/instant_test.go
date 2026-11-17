//go:build !canal

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent/fakeportal"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant/instanttest"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel/tunneltest"
)

// fakeDialect talks to the instanttest driver; it replaces dialect.For in these tests.
type fakeDialect struct{ engine string }

func (fakeDialect) Driver() string                                   { return instanttest.Driver }
func (fakeDialect) DSN(db config.Database, _ string) (string, error) { return db.Name, nil }
func (fakeDialect) Objects() string                                  { return "OBJECTS" }
func (fakeDialect) Columns() string                                  { return "COLUMNS" }
func (fakeDialect) WritePrivileges() string                          { return "WRITES" }
func (fakeDialect) Quote(s string) string                            { return `"` + s + `"` }
func (fakeDialect) Kind(native string, _ sql.NullInt64) instant.Kind {
	return map[string]instant.Kind{"varchar": instant.KindString, "int": instant.KindInteger}[native]
}
func (fakeDialect) BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}
func (fakeDialect) SelectByKey(cols []string, from, key string, orderBy []string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1 LIMIT %d", strings.Join(cols, ", "), from, key, limit)
}

// instantSetup writes config/node.yaml and config/services/afiliacion.yaml (engine, without
// resources unless given) over a fresh fake database, and swaps dialect.For.
func instantSetup(t *testing.T, portal, engine, resources string) (configPath, serviceFile string, db *instanttest.DB) {
	t.Helper()
	old := dialectFor
	dialectFor = func(e string) (instant.Dialect, error) { return fakeDialect{e}, nil }
	t.Cleanup(func() { dialectFor = old })
	name := strings.ReplaceAll(t.Name(), "/", "_")
	db = instanttest.New(name)
	db.Objects = [][]driver.Value{{"SALUD", "V_AFILIADOS", "view"}}
	db.Columns["SALUD.V_AFILIADOS"] = [][]driver.Value{
		{"NUMERO_DOCUMENTO", "varchar", int64(12), nil, "NO"},
		{"NOMBRES", "varchar", int64(80), nil, "YES"},
		{"FOTO", "blob", nil, nil, "YES"},
	}
	db.Rows["123"] = [][]driver.Value{{"123", "Ana", nil}}
	dir := t.TempDir()
	secret := writeFile(t, dir, "db_password", "s3creto", 0o600)
	configPath = writeFile(t, dir, "node.yaml", "portal: "+portal+"\n", 0o600)
	_ = os.Mkdir(filepath.Join(dir, "services"), 0o755)
	serviceFile = writeFile(t, filepath.Join(dir, "services"), "afiliacion.yaml", fmt.Sprintf(`name: afiliacion
path: /salud
type: instant
database: { engine: %s, host: bd.interna, port: 5432, name: %s, user: rid_reader, passwordFile: %s }
%s`, engine, name, filepath.ToSlash(secret), resources), 0o644)
	return configPath, serviceFile, db
}

const afiliadosYAML = `resources:
  - name: afiliados
    entity: Afiliado
    source: salud.v_afiliados
    key: { column: numero_documento }
    dataSubject: { documentType: CC }
    columns: [{ name: numero_documento }, { name: nombres }]
`

func cli(t *testing.T, configPath string, args ...string) (int, string) {
	t.Helper()
	t.Setenv("RID_CONFIG", configPath)
	var out bytes.Buffer
	code := run(args, &out)
	return code, out.String()
}

func TestCatalogCommand(t *testing.T) {
	cfg, svc, _ := instantSetup(t, "https://portal.red.example", "postgres", "")
	code, out := cli(t, cfg, "catalog", "--service-file", svc)
	if code != 0 || !strings.Contains(out, "SALUD.V_AFILIADOS") || !strings.Contains(out, "NUMERO_DOCUMENTO") ||
		!strings.Contains(out, "FOTO") || !strings.Contains(out, "no se puede exponer") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestScaffoldAppendsResource(t *testing.T) {
	cfg, svc, _ := instantSetup(t, "https://portal.red.example", "postgres", "")
	code, out := cli(t, cfg, "scaffold", "--service-file", svc, "--source", "salud.v_afiliados", "--key", "numero_documento", "--subject", "CC")
	if code != 0 || !strings.Contains(out, "foto") {
		t.Fatalf("code=%d (the skipped column must be named)\n%s", code, out)
	}
	s, err := config.LoadServiceFile(svc)
	if err != nil {
		t.Fatal(err)
	}
	r := s.Instant.Resources[0]
	if r.Name != "afiliados" || r.Entity != "Afiliados" || r.Source != "salud.v_afiliados" || r.Key.Column != "numero_documento" ||
		r.DataSubject == nil || r.DataSubject.DocumentType != "CC" || len(r.Columns) != 2 || r.Columns[1].Name != "nombres" || r.Many {
		t.Fatalf("%+v", r)
	}
	// A second resource with another name is appended; the same name is refused.
	if code, out := cli(t, cfg, "scaffold", "--service-file", svc, "--source", "salud.v_afiliados", "--key", "numero_documento",
		"--resource", "historial", "--entity", "Novedad", "--many"); code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if code, out := cli(t, cfg, "scaffold", "--service-file", svc, "--source", "salud.v_afiliados", "--key", "numero_documento"); code == 0 || !strings.Contains(out, "afiliados") {
		t.Fatalf("a repeated resource must be refused: code=%d\n%s", code, out)
	}
	s, _ = config.LoadServiceFile(svc)
	if len(s.Instant.Resources) != 2 || !s.Instant.Resources[1].Many || s.Instant.Resources[1].Entity != "Novedad" {
		t.Fatalf("%+v", s.Instant.Resources)
	}
	// The skipped columns stay in the file, as a comment over the resource.
	if raw, _ := os.ReadFile(svc); !strings.Contains(string(raw), "# ") || !strings.Contains(string(raw), "foto") {
		t.Fatalf("the skipped column is not noted in the file:\n%s", raw)
	}
	// --print (the installer: the container's config is read-only) prints the new file and leaves it alone.
	before, _ := os.ReadFile(svc)
	code, printed := cli(t, cfg, "scaffold", "--print", "--service-file", svc, "--source", "salud.v_afiliados", "--key", "numero_documento", "--resource", "tercero", "--entity", "Tercero")
	if after, _ := os.ReadFile(svc); code != 0 || !bytes.Equal(before, after) {
		t.Fatalf("--print must not write: code=%d\n%s", code, printed)
	}
	p := filepath.Join(t.TempDir(), "printed.yaml")
	_ = os.WriteFile(p, []byte(printed), 0o600)
	if s, err := config.LoadServiceFile(p); err != nil || len(s.Instant.Resources) != 3 || s.Instant.Resources[2].Name != "tercero" {
		t.Fatalf("--print must print the whole valid file: %v\n%s", err, printed)
	}
	for _, bad := range [][]string{{"--source", "salud.no_existe", "--key", "id"}, {"--source", "salud.v_afiliados", "--key", "foto"}} {
		before, _ := os.ReadFile(svc)
		args := append([]string{"scaffold", "--service-file", svc, "--resource", "otro", "--entity", "Otro"}, bad...)
		if code, _ := cli(t, cfg, args...); code == 0 {
			t.Fatalf("%v must fail", bad)
		}
		if after, _ := os.ReadFile(svc); !bytes.Equal(before, after) {
			t.Fatalf("%v: a failed scaffold must leave the file as it was", bad)
		}
	}
}

func TestOpenAPICommand(t *testing.T) {
	cfg, _, _ := instantSetup(t, "https://portal.red.example", "postgres", afiliadosYAML)
	code, out := cli(t, cfg, "openapi", "afiliacion")
	var doc map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc["openapi"] != "3.1.0" || !strings.Contains(out, "getAfiliado") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if code, out := cli(t, cfg, "openapi", "otro"); code == 0 || !strings.Contains(out, "otro") {
		t.Fatalf("unknown service: code=%d %s", code, out)
	}
}

func TestValidateCompilesInstant(t *testing.T) {
	cfg, _, db := instantSetup(t, "https://portal.red.example", "postgres", afiliadosYAML)
	var out bytes.Buffer
	if code := validate(cfg, &out); code != 0 || !strings.Contains(out.String(), "compila") {
		t.Fatalf("code=%d %s", code, out.String())
	}
	db.Set(func(f *instanttest.DB) { f.Down = true })
	out.Reset()
	if code := validate(cfg, &out); code != 0 || !strings.Contains(out.String(), "⚠") {
		t.Fatalf("a database that does not answer is a warning (the node retries): code=%d %s", code, out.String())
	}
	cfg2, _, _ := instantSetup(t, "https://portal.red.example", "postgres", strings.Replace(afiliadosYAML, "nombres", "no_existe", 1))
	out.Reset()
	if code := validate(cfg2, &out); code != 1 || !strings.Contains(out.String(), "no_existe") {
		t.Fatalf("a declaration that does not compile fails: code=%d %s", code, out.String())
	}
	cfg3, _, _ := instantSetup(t, "https://portal.red.example", "postgres", "")
	out.Reset()
	if code := validate(cfg3, &out); code != 0 || !strings.Contains(out.String(), "todavía no tiene recursos") {
		t.Fatalf("no resources yet: a warning: code=%d %s", code, out.String())
	}
}

func TestDoctorInstant(t *testing.T) {
	portal := portalWithDate(t, 0)
	cfg, _, db := instantSetup(t, portal.URL, "postgres", afiliadosYAML)
	code, out := doctorAt(t, portal, cfg)
	if code != 0 || !strings.Contains(out, "✔ servicio afiliacion: conexión con la base de datos") || !strings.Contains(out, "no puede escribir en las fuentes expuestas") || !strings.Contains(out, "solo lectura") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	db.Set(func(f *instanttest.DB) { f.Writes = 1 })
	code, out = doctorAt(t, portal, cfg)
	if code != 1 || !strings.Contains(out, "✘") || !strings.Contains(out, "salud.v_afiliados") {
		t.Fatalf("a user that can write is a ✘ naming the source: code=%d\n%s", code, out)
	}
	db.Set(func(f *instanttest.DB) { f.Writes = 0; f.Down = true })
	if code, out = doctorAt(t, portal, cfg); code != 1 || !strings.Contains(out, "no se pudo conectar") {
		t.Fatalf("database down: code=%d\n%s", code, out)
	}
	cfgSQL, _, _ := instantSetup(t, portal.URL, "sqlserver", afiliadosYAML)
	if _, out = doctorAt(t, portal, cfgSQL); !strings.Contains(out, "ℹ") {
		t.Fatalf("SQL Server has no read-only transaction: an ℹ\n%s", out)
	}
}

func doctorAt(t *testing.T, portal interface{ Client() *http.Client }, cfg string) (int, string) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	_ = os.Mkdir(stateDir, 0o700)
	var out bytes.Buffer
	code := doctor(context.Background(), doctorOptions{ConfigPath: cfg, StateDir: stateDir, TokenFile: filepath.Join(stateDir, "token"),
		PortalHTTP: portal.Client(), Now: time.Now}, &out)
	// No node is running in these tests: its status and the token are not what is checked here.
	return code, out.String()
}

func TestRunServesInstantInProcess(t *testing.T) {
	p := fakeportal.New(t)
	nodeAddr := netip.MustParsePrefix("10.77.0.3/32")
	var dial func(context.Context, netip.AddrPort) (net.Conn, error)
	p.OnEnroll = func(req contract.EnrollRequest) contract.WireGuard {
		nodePub, _ := tunnel.ParseKey(req.WireGuardPublicKey)
		var hubPub tunnel.Key
		var endpoint string
		hubPub, endpoint, dial = tunneltest.Hub(t, nodePub, nodeAddr)
		return contract.WireGuard{Address: nodeAddr.String(), HubPublicKey: hubPub.String(), HubEndpoint: endpoint,
			HubAddress: tunneltest.HubAddress.String(), AllowedIPs: []string{tunneltest.HubAddress.String() + "/32"}, PersistentKeepaliveSeconds: 25}
	}
	cfg, _, _ := instantSetup(t, p.URL(), "postgres", afiliadosYAML)
	raw, _ := os.ReadFile(cfg)
	_ = os.WriteFile(cfg, append(raw, []byte("controlDialAddress: "+p.DialAddress()+"\n")...), 0o600)
	dir := filepath.Dir(cfg)
	token := writeFile(t, dir, "token", p.Token, 0o600)
	stateDir := filepath.Join(dir, "state")
	_ = os.Mkdir(stateDir, 0o700)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runNode(ctx, runOptions{ConfigPath: cfg, StateDir: stateDir, TokenFile: token, PortalRoots: p.PKI.Roots,
			HeartbeatEvery: 50 * time.Millisecond, Log: slog.New(slog.DiscardHandler)})
	}()
	// The heartbeat declares the service with the SHA of its generated OpenAPI once it compiled.
	var sha string
	for deadline := time.Now().Add(10 * time.Second); sha == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		for _, r := range p.Requests() {
			var hb contract.HeartbeatRequest
			if strings.HasSuffix(r.Path, "/heartbeat") && json.Unmarshal(r.Body, &hb) == nil && len(hb.Services) == 1 {
				sha = hb.Services[0].OpenAPISHA256
			}
		}
	}
	if sha == "" {
		t.Fatal("no heartbeat declared the instant service with its OpenAPI SHA")
	}
	gw := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{ServerName: p.NodeName, RootCAs: p.PKI.Roots, Certificates: []tls.Certificate{p.PKI.Gateway}},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, netip.AddrPortFrom(nodeAddr.Addr(), 8443))
		},
	}}
	res, err := gw.Get("https://" + p.NodeName + ":8443/salud/afiliados/123")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"nombres":"Ana"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	_, openapi := cli(t, cfg, "openapi", "afiliacion")
	if sum := sha256.Sum256([]byte(openapi)); hex.EncodeToString(sum[:]) != sha {
		t.Fatalf("the heartbeat's SHA %s is not the SHA of `openapi`'s output", sha)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runNode: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runNode did not stop")
	}
}

// doctor opens a read-only transaction instead of assuming the engine has one.
func TestDoctorOpensTheReadOnlyTransaction(t *testing.T) {
	portal := portalWithDate(t, 0)
	cfg, _, db := instantSetup(t, portal.URL, "postgres", afiliadosYAML)
	db.Set(func(f *instanttest.DB) { f.ReadOnlyErr = errors.New("cannot set transaction read-only mode") })
	code, out := doctorAt(t, portal, cfg)
	if code != 1 || !strings.Contains(out, "✘ servicio afiliacion: no se pudo abrir una transacción de solo lectura") {
		t.Fatalf("code=%d %s", code, out)
	}
}

// The pool stays open while the server drains: it closes when run is done with it, not when the
// context ends.
func TestInstantRuntimeClosesAfterServing(t *testing.T) {
	cfg, _, _ := instantSetup(t, "https://portal.red.example", "postgres", afiliadosYAML)
	n, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	route, _, closeFn, err := instantRuntime(ctx, n.Services[0], slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	get := func() int {
		rec := httptest.NewRecorder()
		route.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/afiliados/123", nil))
		return rec.Code
	}
	for deadline := time.Now().Add(3 * time.Second); get() != 200; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("never served")
		}
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
	if code := get(); code != 200 {
		t.Fatalf("a request after shutdown began (the server drains) got %d", code)
	}
	closeFn()
	if code := get(); code != 503 {
		t.Fatalf("after close: %d", code)
	}
}
