//go:build integration && !canal

// Integration with the four engines of test/engines (make test-engines). It needs the users'
// passwords in RID_IT_READER_PASSWORD and RID_IT_WRITER_PASSWORD; RID_IT_ENGINES narrows the
// engines (comma-separated).
package instant_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant/dialect"
)

type engineIT struct {
	engine string
	port   int
	name   string // database (MySQL: the schema; Oracle: the service)
	tls    string
}

var engines = []engineIT{
	{"postgres", 55432, "entidad", "disable"},
	{"mysql", 53306, "salud", "require"},
	{"sqlserver", 51433, "entidad", "require"},
	{"oracle", 51521, "FREEPDB1", "disable"},
}

func forEachEngine(t *testing.T, fn func(t *testing.T, e engineIT)) {
	if os.Getenv("RID_IT_READER_PASSWORD") == "" || os.Getenv("RID_IT_WRITER_PASSWORD") == "" {
		t.Skip("RID_IT_READER_PASSWORD and RID_IT_WRITER_PASSWORD are not set (make test-engines)")
	}
	only := os.Getenv("RID_IT_ENGINES")
	for _, e := range engines {
		if only != "" && !slices.Contains(strings.Split(only, ","), e.engine) {
			continue
		}
		t.Run(e.engine, func(t *testing.T) { fn(t, e) })
	}
}

func database(t *testing.T, e engineIT, user, password string) *config.Database {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "db_password")
	if err := os.WriteFile(secret, []byte(password), 0o600); err != nil {
		t.Fatal(err)
	}
	return &config.Database{Engine: e.engine, Host: "127.0.0.1", Port: e.port, Name: e.name, User: user,
		PasswordFile: secret, TLS: e.tls, MaxConnections: 4, QueryTimeout: 10 * time.Second}
}

func open(t *testing.T, e engineIT, user, password string, resources ...config.Resource) *instant.Service {
	t.Helper()
	cfg := config.Service{Name: "afiliacion", Path: "/salud", Type: "instant", Instant: config.InstantSpec{
		Title: "Afiliación", Version: "1.0.0", Description: "Prueba de integración",
		Database: database(t, e, user, password), Resources: resources,
	}}
	d, err := dialect.For(e.engine)
	if err != nil {
		t.Fatal(err)
	}
	s, err := instant.Open(cfg, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func reader(t *testing.T, e engineIT, resources ...config.Resource) *instant.Service {
	return open(t, e, "rid_reader", os.Getenv("RID_IT_READER_PASSWORD"), resources...)
}

func compile(t *testing.T, s *instant.Service) {
	t.Helper()
	if err := s.Compile(context.Background()); err != nil {
		t.Fatalf("compile: %v (%v)", err, errors_unwrap(err))
	}
}

func errors_unwrap(err error) error {
	if u, ok := err.(interface{ Unwrap() error }); ok {
		return u.Unwrap()
	}
	return nil
}

func afiliados() config.Resource {
	return config.Resource{Name: "afiliados", Entity: "Afiliado", Source: "salud.v_afiliados", MaxRows: 100,
		Description: "Afiliado por número de documento",
		Key:         config.KeySpec{Column: "numero_documento", Description: "Número del documento"},
		DataSubject: &config.SubjectSpec{DocumentType: "CC"},
		Columns: []config.ColumnSpec{{Name: "tipo_documento"}, {Name: "numero_documento"}, {Name: "eps"}, {Name: "regimen"},
			{Name: "estado"}, {Name: "fecha_afiliacion", As: "date"}, {Name: "actualizado_en"}}}
}

func hogares() config.Resource {
	return config.Resource{Name: "hogares", Entity: "Hogar", Source: "salud.v_hogares", Many: true, MaxRows: 10,
		Key:     config.KeySpec{Column: "hogar_id"},
		Columns: []config.ColumnSpec{{Name: "hogar_id"}, {Name: "numero_documento"}, {Name: "puntaje"}, {Name: "activo", As: "boolean"}}}
}

func tipos() config.Resource {
	return config.Resource{Name: "tipos", Entity: "Tipos", Source: "salud.v_tipos", MaxRows: 100,
		Key: config.KeySpec{Column: "id"},
		Columns: []config.ColumnSpec{{Name: "decimal_largo"}, {Name: "entero_grande"}, {Name: "fecha", As: "date"},
			{Name: "fecha_hora"}, {Name: "fecha_hora_zona"}, {Name: "texto"}, {Name: "bandera", As: "boolean"}, {Name: "identificador"}}}
}

func get(t *testing.T, s *instant.Service, target string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Code, rec.Body.Bytes()
}

func decode(t *testing.T, b []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
}

func TestCatalogOnlyGranted(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		cat, err := reader(t, e).Catalog(context.Background())
		if err != nil {
			t.Fatalf("%v (%v)", err, errors_unwrap(err))
		}
		var got []string
		for _, o := range cat {
			got = append(got, strings.ToLower(o.Schema+"."+o.Name))
		}
		slices.Sort(got)
		if want := []string{"salud.v_afiliados", "salud.v_codigos", "salud.v_hogares", "salud.v_tipos"}; !slices.Equal(got, want) {
			t.Fatalf("the reader sees %v, want %v", got, want)
		}
	})
}

func TestServeByKey(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		s := reader(t, e, afiliados(), hogares())
		compile(t, s)
		code, body := get(t, s, "/afiliados/1000000001")
		var one map[string]any
		decode(t, body, &one)
		if code != 200 || one["numero_documento"] != "1000000001" || one["tipo_documento"] != "CC" || one["eps"] != "EPS Dos" ||
			one["fecha_afiliacion"] != "2025-11-25" || len(one) != 7 {
			t.Fatalf("existing key: %d %s", code, body)
		}
		for target, want := range map[string]int{
			"/afiliados/9999999999":                 404,
			"/afiliados/abc'--":                     400,
			"/afiliados/1000000001?campo=x":         400,
			"/afiliados":                            404,
			"/afiliados/1000000001/extra":           404,
			"/afiliados/" + strings.Repeat("9", 21): 400,
		} {
			if code, body := get(t, s, target); code != want {
				t.Errorf("%s: %d %s, want %d", target, code, body, want)
			}
		}
		code, body = get(t, s, "/hogares/1")
		var many []map[string]any
		decode(t, body, &many)
		if code != 200 || len(many) != 2 || many[0]["activo"] != true || many[0]["hogar_id"] != json.Number("1") {
			t.Fatalf("many: %d %s", code, body)
		}
		if code, body := get(t, s, "/"); code != 200 {
			t.Fatalf("root: %d %s", code, body)
		}
		writeOpenAPI(t, e, s)
	})
}

// writeOpenAPI leaves the OpenAPI for the portal lint (make test-engines runs it).
func writeOpenAPI(t *testing.T, e engineIT, s *instant.Service) {
	t.Helper()
	b, err := s.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, e.engine+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTypesRoundTrip(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		s := reader(t, e, tipos())
		compile(t, s)
		code, body := get(t, s, "/tipos/1")
		var row map[string]any
		decode(t, body, &row)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		// The exact value, digit for digit (Oracle's NUMBER keeps no trailing zeros: same value).
		dec, _ := row["decimal_largo"].(json.Number)
		got, ok1 := new(big.Rat).SetString(string(dec))
		exact, _ := new(big.Rat).SetString("12345678901234567890.1234567890")
		if !ok1 || got.Cmp(exact) != 0 {
			t.Errorf("decimal_largo = %#v, want 12345678901234567890.1234567890 exactly", row["decimal_largo"])
		}
		want := map[string]any{
			"entero_grande": json.Number("9007199254740993"),
			"fecha":         "2026-11-16",
			"fecha_hora":    "2026-11-16T10:30:15.123",
			"texto":         "José \"Pepe\" O'Brien\nñandú ✓",
			"bandera":       true,
			"identificador": "6f1c0b2a-3d4e-4f50-8a6b-7c8d9e0f1a2b",
		}
		for k, v := range want {
			if row[k] != v {
				t.Errorf("%s = %#v, want %#v", k, row[k], v)
			}
		}
		zoned, _ := row["fecha_hora_zona"].(string)
		if e.engine == "mysql" { // no time zone type: a DATETIME in UTC
			if zoned != "2026-11-16T15:30:15.123" {
				t.Errorf("fecha_hora_zona = %q", zoned)
			}
		} else if ts, err := time.Parse(time.RFC3339Nano, zoned); err != nil || !ts.Equal(time.Date(2026, 11, 16, 15, 30, 15, 123e6, time.UTC)) {
			t.Errorf("fecha_hora_zona = %q (%v)", zoned, err)
		}
		code, body = get(t, s, "/tipos/2")
		decode(t, body, &row)
		for k, v := range row {
			if v != nil {
				t.Errorf("NULL %s came back as %#v", k, v)
			}
		}
		if code != 200 || !json.Valid(body) {
			t.Fatalf("%d %s", code, body)
		}

		bin := tipos()
		bin.Columns = append(bin.Columns, config.ColumnSpec{Name: "binario"})
		err := reader(t, e, bin).Compile(context.Background())
		if err == nil || !strings.Contains(err.Error(), `"binario"`) {
			t.Fatalf("a binary column is rejected when compiling, naming it: %v", err)
		}
	})
}

func writerDB(t *testing.T, e engineIT) (*sql.DB, instant.Dialect) {
	t.Helper()
	d, _ := dialect.For(e.engine)
	dsn, err := d.DSN(*database(t, e, "rid_writer", os.Getenv("RID_IT_WRITER_PASSWORD")), os.Getenv("RID_IT_WRITER_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(d.Driver(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, d
}

func TestReadOnlyTxBlocksWrites(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		if e.engine == "sqlserver" {
			t.Skip("SQL Server has no read-only transaction: the guarantee is the read-only user (doctor checks it)")
		}
		db, d := writerDB(t, e)
		ctx := context.Background()
		ph := map[string]string{"postgres": "$1", "mysql": "?", "oracle": ":1"}[e.engine]
		insert := "INSERT INTO salud.escritura (id, nota) VALUES (" + ph + ", 'x')"
		del := "DELETE FROM salud.escritura WHERE id = " + ph
		// The writer can write outside the read-only transaction: the test means something.
		if _, err := db.ExecContext(ctx, insert, 1); err != nil {
			t.Fatalf("the writer must be able to write: %v", err)
		}
		if _, err := db.ExecContext(ctx, del, 1); err != nil {
			t.Fatal(err)
		}
		tx, err := d.BeginReadOnly(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(ctx, insert, 2)
		_ = tx.Rollback()
		if err == nil {
			t.Fatal("an INSERT inside the read-only transaction must fail")
		}
		var n int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM salud.escritura WHERE id = "+ph, 2).Scan(&n); err != nil || n != 0 {
			t.Fatalf("row written: %d %v", n, err)
		}
	})
}

func TestWriteCheckFlagsWriter(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		ctx := context.Background()
		r := reader(t, e, afiliados())
		compile(t, r)
		if w, err := r.WriteCheck(ctx); err != nil || len(w) != 0 {
			t.Fatalf("the reader cannot write: %v %v", w, err)
		}
		w := open(t, e, "rid_writer", os.Getenv("RID_IT_WRITER_PASSWORD"), config.Resource{Name: "escritura", Entity: "Escritura",
			Source: "salud.escritura", MaxRows: 100, Key: config.KeySpec{Column: "id"},
			Columns: []config.ColumnSpec{{Name: "id"}, {Name: "nota"}}})
		compile(t, w)
		got, err := w.WriteCheck(ctx)
		if err != nil || !slices.Equal(got, []string{"salud.escritura"}) {
			t.Fatalf("the writer must be flagged: %v %v", got, err)
		}
	})
}

// Final review I4: a CHAR(10) key ("ABC" stored blank-padded) answers on every engine.
func TestCharKey(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		s := reader(t, e, config.Resource{Name: "codigos", Entity: "Codigo", Source: "salud.v_codigos", MaxRows: 100,
			Key: config.KeySpec{Column: "codigo"}, Columns: []config.ColumnSpec{{Name: "nombre"}}})
		compile(t, s)
		code, body := get(t, s, "/codigos/ABC")
		if code != 200 || !strings.Contains(string(body), `"nombre":"Uno"`) {
			t.Fatalf("%d %s", code, body)
		}
	})
}

// Final review I6: writers through a column-level grant (PostgreSQL, MySQL) or a role (MySQL)
// are flagged too.
func TestWriteCheckFlagsIndirectWriters(t *testing.T) {
	escritura := config.Resource{Name: "escritura", Entity: "Escritura", Source: "salud.escritura", MaxRows: 100,
		Key: config.KeySpec{Column: "id"}, Columns: []config.ColumnSpec{{Name: "id"}, {Name: "nota"}}}
	users := map[string][]string{"postgres": {"rid_column_writer"}, "mysql": {"rid_column_writer", "rid_role_writer"}}
	forEachEngine(t, func(t *testing.T, e engineIT) {
		if users[e.engine] == nil {
			t.Skip("no indirect writer in this engine's seed")
		}
		for _, user := range users[e.engine] {
			w := open(t, e, user, os.Getenv("RID_IT_WRITER_PASSWORD"), escritura)
			compile(t, w)
			got, err := w.WriteCheck(context.Background())
			if err != nil || !slices.Equal(got, []string{"salud.escritura"}) {
				t.Fatalf("%s must be flagged: %v %v", user, got, err)
			}
		}
	})
}

// Final review I3: tls: disable works against SQL Server's default self-signed certificate.
func TestSQLServerTLSDisable(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engineIT) {
		if e.engine != "sqlserver" {
			t.Skip("SQL Server only")
		}
		e.tls = "disable"
		compile(t, reader(t, e, afiliados()))
	})
}
