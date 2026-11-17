//go:build !canal

package instant

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant/instanttest"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

const nodeName = "den-node-test"

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type fixture struct {
	svc  *Service
	db   *instanttest.DB
	logs *syncBuffer
	h    http.Handler
}

// open starts a service on a fresh fake database holding SALUD.V_AFILIADOS.
func open(t *testing.T, prepare func(f *instanttest.DB), resources ...config.Resource) *fixture {
	t.Helper()
	return openWith(t, fakeDialect{}, prepare, resources...)
}

func openWith(t *testing.T, d Dialect, prepare func(f *instanttest.DB), resources ...config.Resource) *fixture {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	f := instanttest.New(name)
	f.Objects = [][]driver.Value{{"SALUD", "V_AFILIADOS", "view"}}
	f.Columns["SALUD.V_AFILIADOS"] = [][]driver.Value{
		{"NUMERO_DOCUMENTO", "varchar", int64(12), nil, "NO"},
		{"NOMBRES", "varchar", int64(80), nil, "YES"},
		{"ACTIVO", "int", nil, int64(0), "NO"},
		{"ID", "integer", nil, int64(0), "YES"},
		{"FECHA", "date", nil, nil, "YES"},
		{"PUNTAJE", "numeric", nil, int64(2), "YES"},
		{"NUM", "NUMBER", nil, int64(0), "YES"},
	}
	f.Rows["123"] = [][]driver.Value{{"123", []byte("Ana"), int64(1)}}
	f.Rows["dup"] = [][]driver.Value{{"dup", "A", int64(1)}, {"dup", "B", int64(0)}}
	f.Rows["tres"] = [][]driver.Value{{"tres", "A", int64(1)}, {"tres", "B", int64(1)}, {"tres", "C", int64(1)}}
	if prepare != nil {
		prepare(f)
	}
	if len(resources) == 0 {
		resources = []config.Resource{{Name: "afiliados", Entity: "Afiliado", Source: "salud.v_afiliados", MaxRows: 100,
			Key:     config.KeySpec{Column: "numero_documento"},
			Columns: []config.ColumnSpec{{Name: "numero_documento"}, {Name: "nombres"}, {Name: "activo", As: "boolean"}}}}
	}
	secret := filepath.Join(t.TempDir(), "db_password")
	if err := os.WriteFile(secret, []byte("s3creto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Service{Name: "afiliacion", Path: "/salud", Type: "instant", Instant: config.InstantSpec{
		Title: "Afiliación", Version: "1.0.0", Resources: resources,
		Database: &config.Database{Engine: "postgres", Name: name, PasswordFile: secret, MaxConnections: 4, QueryTimeout: 2 * time.Second},
	}}
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	s, err := Open(cfg, d, log)
	if err != nil {
		t.Fatal(err)
	}
	s.retryMin, s.retryMax, s.recompileEvery = 10*time.Millisecond, 50*time.Millisecond, 0
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.Close() })
	s.Start(ctx)
	h := server.NewHandler(nodeName, []server.Route{{Path: "/salud", Handler: s.Handler()}}, log)
	return &fixture{svc: s, db: f, logs: logs, h: h}
}

func (fx *fixture) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !fx.svc.Ready() {
		if time.Now().After(deadline) {
			t.Fatalf("not ready: %v", fx.svc.Err())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (fx *fixture) get(t *testing.T, target string) (int, map[string]any, []byte) {
	t.Helper()
	return fx.do(t, http.MethodGet, target)
}

func (fx *fixture) do(t *testing.T, method, target string) (int, map[string]any, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, "https://"+nodeName+target, nil)
	rec := httptest.NewRecorder()
	fx.h.ServeHTTP(rec, req)
	body := rec.Body.Bytes()
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return rec.Code, m, body
}

func TestHandlerOneRow(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	code, m, body := fx.get(t, "/salud/afiliados/123")
	if code != 200 || m["numero_documento"] != "123" || m["nombres"] != "Ana" || m["activo"] != true || len(m) != 3 {
		t.Fatalf("%d %s", code, body)
	}
	if fx.db.ReadOnly == 0 {
		t.Fatal("the query must run in a read-only transaction")
	}
	if !strings.Contains(fx.logs.String(), `"/salud/afiliados/{numero_documento}"`) || strings.Contains(fx.logs.String(), "/afiliados/123") {
		t.Fatalf("the access log must carry the template, never the key:\n%s", fx.logs)
	}
}

func TestHandlerQueryStringRejected(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	for _, target := range []string{"/salud/afiliados/123?x=1", "/salud/afiliados/123?"} {
		if code, m, _ := fx.get(t, target); code != 400 || m["code"] != "query_not_allowed" {
			t.Fatalf("%s: %d %v", target, code, m)
		}
	}
	if fx.db.ByKey != 0 {
		t.Fatal("no query may run")
	}
}

func TestHandlerInvalidKey(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	for _, target := range []string{"/salud/afiliados/12%2034", "/salud/afiliados/1234567890123", "/salud/afiliados/12'--"} {
		if code, m, _ := fx.get(t, target); code != 400 || m["code"] != "invalid_key" {
			t.Fatalf("%s: %d %v", target, code, m)
		}
	}
	if fx.db.ByKey != 0 {
		t.Fatal("no query may run")
	}
}

func TestHandlerNotFound(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	for _, target := range []string{"/salud/afiliados/999", "/salud/otros/1", "/salud/afiliados", "/salud/afiliados/1/x"} {
		if code, m, _ := fx.get(t, target); code != 404 || m["code"] != "not_found" {
			t.Fatalf("%s: %d %v", target, code, m)
		}
	}
	if code, _, _ := fx.do(t, http.MethodPost, "/salud/afiliados/123"); code != 405 {
		t.Fatalf("POST: %d", code)
	}
}

func TestHandlerNonUniqueKey(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	if code, m, _ := fx.get(t, "/salud/afiliados/dup"); code != 500 || m["code"] != "non_unique_key" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestHandlerMany(t *testing.T) {
	fx := open(t, nil, config.Resource{Name: "historial", Entity: "Novedad", Source: "salud.v_afiliados", Many: true, MaxRows: 2,
		Key: config.KeySpec{Column: "numero_documento"}, Columns: []config.ColumnSpec{{Name: "nombres"}}})
	fx.waitReady(t)
	code, _, body := fx.get(t, "/salud/historial/dup")
	var list []map[string]any
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || len(list) != 2 || list[1]["nombres"] != "B" {
		t.Fatalf("%d %s", code, body)
	}
	if code, m, _ := fx.get(t, "/salud/historial/tres"); code != 500 || m["code"] != "too_many_rows" {
		t.Fatalf("more rows than maxRows: %d %v", code, m)
	}
	if code, _, _ := fx.get(t, "/salud/historial/999"); code != 404 {
		t.Fatalf("no rows: %d", code)
	}
}

func TestHandlerDBDown503AndRecovers(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	fx.db.Set(func(f *instanttest.DB) { f.Down = true })
	start := time.Now()
	for i := 0; i < 4; i++ {
		if code, m, _ := fx.get(t, "/salud/afiliados/123"); code != 503 || m["code"] != "service_unavailable" {
			t.Fatalf("db down: %d %v", code, m)
		}
	}
	if time.Since(start) > time.Second {
		t.Fatalf("503 must be fast, took %s", time.Since(start))
	}
	if fx.svc.Probe(context.Background()) == nil {
		t.Fatal("the probe must fail while the database is down")
	}
	fx.db.Set(func(f *instanttest.DB) { f.Down = false })
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, _, _ := fx.get(t, "/salud/afiliados/123")
		if code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not recover: %d", code)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := fx.svc.Probe(context.Background()); err != nil {
		t.Fatalf("probe after recovery: %v", err)
	}
}

func TestStartsWithDBDown(t *testing.T) {
	fx := open(t, func(f *instanttest.DB) { f.Down = true })
	time.Sleep(50 * time.Millisecond)
	if fx.svc.Ready() {
		t.Fatal("cannot be ready without a catalog")
	}
	if code, m, _ := fx.get(t, "/salud/afiliados/123"); code != 503 || m["code"] != "service_unavailable" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _, _ := fx.get(t, "/salud/"); code != 503 {
		t.Fatalf("root: %d", code)
	}
	fx.db.Set(func(f *instanttest.DB) { f.Down = false })
	fx.waitReady(t)
	if code, _, _ := fx.get(t, "/salud/afiliados/123"); code != 200 {
		t.Fatalf("after the database came up: %d", code)
	}
}

func TestRootReflectsDB(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	if code, m, _ := fx.get(t, "/salud/"); code != 200 || m["status"] != "ok" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _, _ := fx.get(t, "/salud"); code != 200 {
		t.Fatalf("without the slash: %d", code)
	}
	fx.db.Set(func(f *instanttest.DB) { f.Down = true })
	if code, _, _ := fx.get(t, "/salud/"); code != 503 {
		t.Fatalf("db down: %d", code)
	}
}

func TestDBErrorNotLogged(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	fx.db.Set(func(f *instanttest.DB) { f.QueryErr = errors.New(`ORA-01722: invalid number "CC-9988776655"`) })
	if code, _, body := fx.get(t, "/salud/afiliados/123"); code != 503 || bytes.Contains(body, []byte("9988776655")) {
		t.Fatalf("%d %s", code, body)
	}
	logs := fx.logs.String()
	if strings.Contains(logs, "9988776655") || strings.Contains(logs, "ORA-01722") {
		t.Fatalf("the driver's message reached the logs:\n%s", logs)
	}
	if !strings.Contains(logs, `"class":"query"`) {
		t.Fatalf("the error class must be logged:\n%s", logs)
	}
}

func catalogReads(fx *fixture) int {
	fx.db.Mu.Lock()
	defer fx.db.Mu.Unlock()
	return fx.db.Catalogue
}

// Connection failures (a failover, a restart) make the service read the catalog again.
func TestRecompileAfterRepeatedErrors(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	before := catalogReads(fx)
	fx.db.Set(func(f *instanttest.DB) { f.QueryErr = instanttest.ErrRefused })
	for i := 0; i < recompileAfter; i++ {
		fx.get(t, "/salud/afiliados/123")
	}
	deadline := time.Now().Add(2 * time.Second)
	for catalogReads(fx) == before {
		if time.Now().After(deadline) {
			t.Fatal("the catalog was not read again")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Final review I1: query errors a consumer can provoke never make the node scan the catalog, and
// connection errors do it at most once per recompileEvery.
func TestRecompileIsBounded(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	fx.svc.recompileEvery = time.Second
	before := catalogReads(fx)
	fx.db.Set(func(f *instanttest.DB) { f.QueryErr = errors.New("invalid input syntax") })
	for i := 0; i < 3*recompileAfter; i++ {
		fx.get(t, "/salud/afiliados/123")
	}
	time.Sleep(100 * time.Millisecond)
	if n := catalogReads(fx); n != before {
		t.Fatalf("query errors made the node read the catalog %d times", n-before)
	}
	fx.db.Set(func(f *instanttest.DB) { f.QueryErr = instanttest.ErrRefused })
	for i := 0; i < 4*recompileAfter; i++ {
		fx.get(t, "/salud/afiliados/123")
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := catalogReads(fx); n-before > 1 {
		t.Fatalf("%d catalog reads within recompileEvery", n-before)
	}
}

// Final review I1: keys that fit the pattern but cannot exist never reach the database.
func TestKeysThatCannotExist(t *testing.T) {
	fx := open(t, nil, config.Resource{Name: "por-fecha", Entity: "PorFecha", Source: "salud.v_afiliados", MaxRows: 100,
		Key: config.KeySpec{Column: "fecha"}, Columns: []config.ColumnSpec{{Name: "fecha"}}},
		config.Resource{Name: "por-id", Entity: "PorId", Source: "salud.v_afiliados", MaxRows: 100,
			Key: config.KeySpec{Column: "id"}, Columns: []config.ColumnSpec{{Name: "id"}}})
	fx.waitReady(t)
	if code, m, _ := fx.get(t, "/salud/por-fecha/2024-02-30"); code != 400 || m["code"] != "invalid_key" {
		t.Fatalf("an impossible date: %d %v", code, m)
	}
	if code, m, _ := fx.get(t, "/salud/por-id/9999999999"); code != 404 || m["code"] != "not_found" {
		t.Fatalf("beyond an integer column's range: %d %v", code, m)
	}
	if fx.db.ByKey != 0 {
		t.Fatal("no query may run")
	}
}

func TestCompileMismatchIsNotReady(t *testing.T) {
	fx := open(t, nil, config.Resource{Name: "otros", Entity: "Otro", Source: "salud.no_existe", MaxRows: 100,
		Key: config.KeySpec{Column: "id"}, Columns: []config.ColumnSpec{{Name: "id"}}})
	deadline := time.Now().Add(2 * time.Second)
	for fx.svc.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fx.svc.Ready() || fx.svc.Err() == nil || !strings.Contains(fx.svc.Err().Error(), "salud.no_existe") {
		t.Fatalf("ready=%v err=%v", fx.svc.Ready(), fx.svc.Err())
	}
	if !strings.Contains(fx.logs.String(), "salud.no_existe") {
		t.Fatalf("a declaration error is logged in full:\n%s", fx.logs)
	}
	if _, err := fx.svc.OpenAPI(); err == nil {
		t.Fatal("no OpenAPI without a compiled catalog")
	}
}

func TestWriteCheck(t *testing.T) {
	fx := open(t, nil)
	fx.waitReady(t)
	if w, err := fx.svc.WriteCheck(context.Background()); err != nil || len(w) != 0 {
		t.Fatalf("%v %v", w, err)
	}
	fx.db.Set(func(f *instanttest.DB) { f.Writes = 2 })
	if w, err := fx.svc.WriteCheck(context.Background()); err != nil || len(w) != 1 || w[0] != "salud.v_afiliados" {
		t.Fatalf("%v %v", w, err)
	}
}

// Final review I2: compiling reads the columns of the declared sources only, not of every object
// the user can see (a broad grant would mean thousands of round trips at each compile).
func TestCompileReadsOnlyDeclaredSources(t *testing.T) {
	fx := open(t, func(f *instanttest.DB) {
		for i := 0; i < 50; i++ {
			f.Objects = append(f.Objects, []driver.Value{"OTRO", fmt.Sprintf("T%d", i), "table"})
		}
	})
	fx.waitReady(t)
	fx.db.Mu.Lock()
	n := fx.db.ColumnReads
	fx.db.Mu.Unlock()
	if n != 1 {
		t.Fatalf("compiling one resource read the columns of %d objects", n)
	}
	cat, err := fx.svc.Catalog(context.Background())
	if err != nil || len(cat) != 51 {
		t.Fatalf("catalog (for catalog and scaffold) still lists everything: %d %v", len(cat), err)
	}
}

type paddingDialect struct{ fakeDialect }

func (paddingDialect) BindKey(c Column, key string) any { return key + "*" }

// The handler binds the key through the dialect when it has a KeyBinder (Oracle CHAR keys).
func TestHandlerUsesKeyBinder(t *testing.T) {
	fx := openWith(t, paddingDialect{}, func(f *instanttest.DB) { f.Rows["123*"] = f.Rows["123"] })
	fx.waitReady(t)
	if code, _, body := fx.get(t, "/salud/afiliados/123"); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
}

// Final review I5: a response larger than maxResponseBytes is refused before it fills the
// container's memory.
func TestResponseTooLarge(t *testing.T) {
	big := strings.Repeat("x", 600)
	fx := open(t, func(f *instanttest.DB) {
		f.Rows["grande"] = [][]driver.Value{{"grande", big, int64(1)}, {"grande", big, int64(1)}}
		f.Rows["chico"] = [][]driver.Value{{"chico", big, int64(1)}}
	}, config.Resource{Name: "historial", Entity: "Novedad", Source: "salud.v_afiliados", Many: true, MaxRows: 10,
		Key: config.KeySpec{Column: "numero_documento"}, Columns: []config.ColumnSpec{{Name: "nombres"}}})
	fx.waitReady(t)
	fx.svc.maxResponseBytes = 1000
	if code, m, _ := fx.get(t, "/salud/historial/grande"); code != 500 || m["code"] != "response_too_large" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _, _ := fx.get(t, "/salud/historial/chico"); code != 200 {
		t.Fatalf("under the cap: %d", code)
	}
}

// A table the catalog lists but the user cannot SELECT (MySQL and Oracle list objects with any
// privilege): the compile says so, instead of every request failing.
func TestCompileChecksSelectPermission(t *testing.T) {
	fx := open(t, func(f *instanttest.DB) { f.ProbeErr = errors.New("ORA-01031: insufficient privileges") })
	deadline := time.Now().Add(2 * time.Second)
	for fx.svc.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	err := fx.svc.Err()
	if fx.svc.Ready() || err == nil || !strings.Contains(err.Error(), "SELECT") || !strings.Contains(err.Error(), "salud.v_afiliados") ||
		strings.Contains(err.Error(), "ORA-01031") {
		t.Fatalf("ready=%v err=%v", fx.svc.Ready(), err)
	}
}

// Values that do not fit the column's schema (NaN, a zero date) are refused, never served
// against the OpenAPI: the response is 500 and the log names the column, never the value.
func TestUnrepresentableValue(t *testing.T) {
	fx := open(t, func(f *instanttest.DB) {
		f.Rows["nan"] = [][]driver.Value{{"nan", "x", int64(1), nil, nil, "NaN", nil}}
		f.Rows["cero"] = [][]driver.Value{{"cero", "x", int64(1), nil, time.Time{}, "1.5", nil}}
	}, config.Resource{Name: "puntajes", Entity: "Puntaje", Source: "salud.v_afiliados", MaxRows: 100,
		Key: config.KeySpec{Column: "numero_documento"}, Columns: []config.ColumnSpec{{Name: "puntaje"}, {Name: "fecha"}}})
	fx.waitReady(t)
	for _, key := range []string{"nan", "cero"} {
		if code, m, _ := fx.get(t, "/salud/puntajes/"+key); code != 500 || m["code"] != "unrepresentable_value" {
			t.Fatalf("%s: %d %v", key, code, m)
		}
	}
	if logs := fx.logs.String(); !strings.Contains(logs, `"column":"puntaje"`) || strings.Contains(logs, "NaN") {
		t.Fatalf("the log names the column, never the value: %s", logs)
	}
}

// Oracle NUMBER(p,0) keys can have up to 38 digits: beyond int64 they are bound as text; an
// integer column that cannot hold the key answers 404 without a query.
func TestLongIntegerKeys(t *testing.T) {
	long := "1234567890123456789012345"
	fx := open(t, func(f *instanttest.DB) { f.Rows[long] = [][]driver.Value{{long, "Ana", int64(1)}} },
		config.Resource{Name: "por-num", Entity: "PorNum", Source: "salud.v_afiliados", MaxRows: 100,
			Key: config.KeySpec{Column: "num"}, Columns: []config.ColumnSpec{{Name: "nombres"}}},
		config.Resource{Name: "por-id", Entity: "PorId", Source: "salud.v_afiliados", MaxRows: 100,
			Key: config.KeySpec{Column: "id"}, Columns: []config.ColumnSpec{{Name: "nombres"}}})
	fx.waitReady(t)
	if code, _, body := fx.get(t, "/salud/por-num/"+long); code != 200 {
		t.Fatalf("NUMBER key with 25 digits: %d %s", code, body)
	}
	if code, m, _ := fx.get(t, "/salud/por-id/"+long); code != 404 {
		t.Fatalf("integer column: %d %v", code, m)
	}
}
