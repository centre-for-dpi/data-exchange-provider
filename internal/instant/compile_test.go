//go:build !canal

package instant

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

// fakeDialect quotes like PostgreSQL and talks to the instanttest driver.
type fakeDialect struct{}

func (fakeDialect) Driver() string                                   { return "ridfake" }
func (fakeDialect) DSN(db config.Database, _ string) (string, error) { return db.Name, nil }
func (fakeDialect) Objects() string                                  { return "OBJECTS" }
func (fakeDialect) Columns() string                                  { return "COLUMNS" }
func (fakeDialect) WritePrivileges() string                          { return "WRITES" }
func (fakeDialect) Quote(s string) string                            { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (fakeDialect) Kind(native string, _ sql.NullInt64) Kind {
	return map[string]Kind{"varchar": KindString, "int": KindInteger, "integer": KindInteger, "NUMBER": KindInteger,
		"numeric": KindNumber, "date": KindDate, "timestamp": KindDateTime}[native]
}
func (fakeDialect) BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}
func (fakeDialect) SelectByKey(cols []string, from, key string, orderBy []string, limit int) string {
	return "SELECT " + strings.Join(cols, ", ") + " FROM " + from + " WHERE " + key + " = $1 LIMIT " + itoa(limit)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// An Oracle-like catalog: everything in upper case.
func upperCatalog() []Object {
	return []Object{{Schema: "SALUD", Name: "V_AFILIADOS", Kind: "view", Columns: []Column{
		{Name: "NUMERO_DOCUMENTO", Native: "VARCHAR2", Kind: KindString, MaxLength: 12},
		{Name: "NOMBRES", Native: "VARCHAR2", Kind: KindString, MaxLength: 80, Nullable: true},
		{Name: "ACTIVO", Native: "NUMBER", Kind: KindInteger},
		{Name: "FECHA_AFILIACION", Native: "DATE", Kind: KindDateTime},
		{Name: "FOTO", Native: "BLOB", Kind: KindUnsupported, Nullable: true},
	}}}
}

func service(resources ...config.Resource) config.Service {
	return config.Service{Name: "afiliacion", Path: "/salud", Type: "instant", Instant: config.InstantSpec{
		Title: "Afiliación", Version: "1.0.0", Resources: resources,
	}}
}

func afiliados(cols ...config.ColumnSpec) config.Resource {
	if len(cols) == 0 {
		cols = []config.ColumnSpec{{Name: "numero_documento"}, {Name: "nombres"}}
	}
	return config.Resource{Name: "afiliados", Entity: "Afiliado", Source: "salud.v_afiliados",
		Key: config.KeySpec{Column: "numero_documento"}, MaxRows: 100, Columns: cols}
}

func TestCompileMatchesCaseInsensitively(t *testing.T) {
	rs, err := Compile(service(afiliados()), upperCatalog(), fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	want := `SELECT "NUMERO_DOCUMENTO", "NOMBRES" FROM "SALUD"."V_AFILIADOS" WHERE "NUMERO_DOCUMENTO" = $1 LIMIT 2`
	if r.Query != want {
		t.Fatalf("query:\n got %s\nwant %s", r.Query, want)
	}
	if r.KeyColumn.Name != "NUMERO_DOCUMENTO" || len(r.Columns) != 2 || r.Limit != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestCompileExactMatchWinsOverCaseInsensitive(t *testing.T) {
	cat := append(upperCatalog(), Object{Schema: "salud", Name: "v_afiliados", Kind: "view", Columns: []Column{
		{Name: "numero_documento", Kind: KindString, MaxLength: 12}, {Name: "nombres", Kind: KindString},
	}})
	rs, err := Compile(service(afiliados()), cat, fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rs[0].Query, `"salud"."v_afiliados"`) {
		t.Fatalf("exact match not preferred: %s", rs[0].Query)
	}
	cat[1].Name = "V_Afiliados"
	if _, err := Compile(service(afiliados()), cat, fakeDialect{}); err == nil || !strings.Contains(err.Error(), "ambig") {
		t.Fatalf("two case-insensitive matches must be ambiguous: %v", err)
	}
}

func TestCompileUnsupportedTypeNamesColumnAndAs(t *testing.T) {
	_, err := Compile(service(afiliados(config.ColumnSpec{Name: "numero_documento"}, config.ColumnSpec{Name: "foto"})), upperCatalog(), fakeDialect{})
	if err == nil || !strings.Contains(err.Error(), `"foto"`) || !strings.Contains(err.Error(), "BLOB") {
		t.Fatalf("unsupported type must name the column and its native type: %v", err)
	}
	_, err = Compile(service(afiliados(config.ColumnSpec{Name: "nombres", As: "boolean"})), upperCatalog(), fakeDialect{})
	if err == nil || !strings.Contains(err.Error(), `"nombres"`) || !strings.Contains(err.Error(), "VARCHAR2") || !strings.Contains(err.Error(), "as: string") {
		t.Fatalf("an impossible as must name the column, its type and the possible as: %v", err)
	}
}

func TestCompileAsOverride(t *testing.T) {
	rs, err := Compile(service(afiliados(
		config.ColumnSpec{Name: "activo", As: "boolean"},
		config.ColumnSpec{Name: "fecha_afiliacion", As: "date"},
		config.ColumnSpec{Name: "numero_documento", As: "string"},
	)), upperCatalog(), fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	got := []Kind{rs[0].Exposed[0], rs[0].Exposed[1], rs[0].Exposed[2]}
	if got[0] != KindBoolean || got[1] != KindDate || got[2] != KindString {
		t.Fatalf("exposed kinds: %v", got)
	}
	for _, bad := range []config.ColumnSpec{{Name: "fecha_afiliacion", As: "date-time"}, {Name: "activo", As: "date"}} {
		if _, err := Compile(service(afiliados(bad)), upperCatalog(), fakeDialect{}); err == nil {
			t.Fatalf("%+v must be rejected", bad)
		}
	}
}

func TestDefaultKeyPattern(t *testing.T) {
	rs, err := Compile(service(afiliados()), upperCatalog(), fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	re := rs[0].KeyRegexp
	for key, ok := range map[string]bool{"1234567890": true, "AB-12": true, "123456789012": true, "1234567890123": false, "12 34": false, " 1234": false, "": false, "12;--": false} {
		if re.MatchString(key) != ok {
			t.Errorf("default string pattern on %q: got %v", key, !ok)
		}
	}
	r := afiliados()
	r.Key.Column = "activo"
	rs, err = Compile(service(r), upperCatalog(), fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	if !rs[0].KeyRegexp.MatchString("42") || rs[0].KeyRegexp.MatchString("4a") {
		t.Fatalf("integer key pattern: %s", rs[0].KeyRegexp)
	}
	r = afiliados()
	r.Key.Pattern = "[0-9]+" // the entity's pattern is anchored
	rs, err = Compile(service(r), upperCatalog(), fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].KeyRegexp.MatchString("12abc") || !rs[0].KeyRegexp.MatchString("12") {
		t.Fatalf("an entity pattern must match the whole key: %s", rs[0].KeyRegexp)
	}
}

func TestQueryUsesOnlyCatalogIdentifiers(t *testing.T) {
	for _, source := range []string{`salud."v_afiliados"`, "salud.v_afiliados;drop", `salud.v_afiliados" --`} {
		r := afiliados()
		r.Source = source
		if _, err := Compile(service(r), upperCatalog(), fakeDialect{}); err == nil || !strings.Contains(err.Error(), "no puede leer") {
			t.Fatalf("source %q is not in the catalog: %v", source, err)
		}
	}
	r := afiliados(config.ColumnSpec{Name: "otra"})
	if _, err := Compile(service(r), upperCatalog(), fakeDialect{}); err == nil || !strings.Contains(err.Error(), `"otra"`) {
		t.Fatalf("an unknown column is an error: %v", err)
	}
}

func TestCompileReportsEveryResource(t *testing.T) {
	a := afiliados(config.ColumnSpec{Name: "foto"})
	b := afiliados()
	b.Name, b.Entity, b.Source = "otros", "Otro", "salud.no_existe"
	_, err := Compile(service(a, b), upperCatalog(), fakeDialect{})
	if err == nil || !strings.Contains(err.Error(), `"afiliados"`) || !strings.Contains(err.Error(), `"otros"`) {
		t.Fatalf("every resource's problem at once: %v", err)
	}
}

func TestManyLimit(t *testing.T) {
	r := afiliados()
	r.Many, r.MaxRows = true, 20
	rs, err := Compile(service(r), upperCatalog(), fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Limit != 21 || !strings.HasSuffix(rs[0].Query, "LIMIT 21") {
		t.Fatalf("many reads one row more than maxRows to detect the overflow: %d %s", rs[0].Limit, rs[0].Query)
	}
}

func TestToJSON(t *testing.T) {
	bogota := time.FixedZone("-05", -5*3600)
	ts := time.Date(2026, 11, 16, 10, 30, 15, 120000000, bogota)
	uuid := [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}
	cases := []struct {
		name string
		col  Column
		as   string
		in   any
		want string // JSON
	}{
		{"null", Column{Kind: KindString}, "", nil, `null`},
		{"string from bytes", Column{Kind: KindString}, "", []byte("José"), `"José"`},
		{"long decimal from bytes", Column{Kind: KindNumber}, "", []byte("12345678901234567890.123456789"), `12345678901234567890.123456789`},
		{"decimal from float", Column{Kind: KindNumber}, "", 0.1, `0.1`},
		{"integer from float", Column{Kind: KindInteger}, "", float64(42), `42`},
		{"big integer from string", Column{Kind: KindInteger}, "", "123456789012345678901234", `123456789012345678901234`},
		{"boolean from integer", Column{Kind: KindInteger}, "boolean", int64(1), `true`},
		{"boolean false from integer", Column{Kind: KindInteger}, "boolean", int64(0), `false`},
		{"boolean from bit bytes", Column{Kind: KindBoolean}, "", []byte{1}, `true`},
		{"date", Column{Kind: KindDate}, "", ts, `"2026-11-16"`},
		{"date from datetime", Column{Kind: KindDateTime}, "date", ts, `"2026-11-16"`},
		{"datetime without zone", Column{Kind: KindDateTime}, "", ts, `"2026-11-16T10:30:15.12"`},
		{"datetime with zone", Column{Kind: KindDateTimeTZ}, "", ts, `"2026-11-16T10:30:15.12-05:00"`},
		{"uuid from array", Column{Kind: KindUUID}, "", uuid, `"12345678-9abc-def0-1234-56789abcdef0"`},
		{"uuid from bytes", Column{Kind: KindUUID}, "", uuid[:], `"12345678-9abc-def0-1234-56789abcdef0"`},
		{"uuid from SQL Server bytes", Column{Kind: KindUUID, Native: "uniqueidentifier"}, "",
			[]byte{0x78, 0x56, 0x34, 0x12, 0xbc, 0x9a, 0xf0, 0xde, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}, `"12345678-9abc-def0-1234-56789abcdef0"`},
		{"uuid from text", Column{Kind: KindUUID}, "", "12345678-9ABC-DEF0-1234-56789ABCDEF0", `"12345678-9abc-def0-1234-56789abcdef0"`},
		{"integer as string", Column{Kind: KindInteger}, "string", int64(7), `"7"`},
		{"decimal as string", Column{Kind: KindNumber}, "string", []byte("1.50"), `"1.50"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(ToJSON(c.col, c.as, c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != c.want {
				t.Fatalf("got %s, want %s", b, c.want)
			}
		})
	}
}

func TestToJSONNeverEmitsInvalidJSONNumbers(t *testing.T) {
	for _, in := range []any{"NaN", "1e", "12,5", []byte("abc")} {
		b, err := json.Marshal(ToJSON(Column{Kind: KindNumber}, "", in))
		if err != nil {
			t.Fatalf("%v: %v", in, err)
		}
		if !json.Valid(b) {
			t.Fatalf("%v gave invalid JSON %s", in, b)
		}
	}
}

// Final review C1: a VARCHAR2(4000) key must not panic (regexp repeat counts stop at 1000).
func TestLongTextKeyDoesNotPanic(t *testing.T) {
	cat := upperCatalog()
	cat[0].Columns[0].MaxLength = 4000
	rs, err := Compile(service(afiliados()), cat, fakeDialect{})
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].KeyRegexp.MatchString(strings.Repeat("a", 257)) || !rs[0].KeyRegexp.MatchString(strings.Repeat("a", 256)) {
		t.Fatalf("a long text key is capped at %d: %s", maxKeyBytes, rs[0].KeyRegexp)
	}
}
