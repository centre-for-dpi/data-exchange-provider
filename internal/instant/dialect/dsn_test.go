//go:build !canal

package dialect

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
	"github.com/centre-for-dpi/data-exchange-provider/internal/testpki"
)

const hardPassword = `p@ss:w/rd?&=#%"' x`

func db(engine, tls string) config.Database {
	return config.Database{Engine: engine, Host: "bd.interna", Port: 1234, Name: "entidad", User: "rid_reader",
		TLS: tls, MaxConnections: 4, QueryTimeout: 5 * time.Second}
}

func caFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, []byte(testpki.NewCA("bd-ca").CertPEM()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFor(t *testing.T) {
	for _, e := range config.Engines {
		if _, err := For(e); err != nil {
			t.Fatalf("%s: %v", e, err)
		}
	}
	if _, err := For("db2"); err == nil {
		t.Fatal("unknown engine")
	}
}

func TestDSN(t *testing.T) {
	ca := caFile(t)
	type check func(t *testing.T, dsn string)
	urlParams := func(want map[string]string) check {
		return func(t *testing.T, dsn string) {
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if p, _ := u.User.Password(); p != hardPassword || u.User.Username() != "rid_reader" {
				t.Fatalf("credentials not escaped: %q", dsn)
			}
			q := u.Query()
			for k, v := range want {
				if got := q.Get(k); got != v {
					t.Errorf("%s=%q, want %q (%s)", k, got, v, dsn)
				}
			}
		}
	}
	cases := []struct {
		engine, tls, ca string
		check           check
	}{
		{"postgres", "disable", "", urlParams(map[string]string{"sslmode": "disable"})},
		{"postgres", "require", "", urlParams(map[string]string{"sslmode": "require"})},
		{"postgres", "verify", "", urlParams(map[string]string{"sslmode": "verify-full", "sslrootcert": ""})},
		{"postgres", "verify", ca, urlParams(map[string]string{"sslmode": "verify-full", "sslrootcert": ca})},
		{"sqlserver", "disable", "", urlParams(map[string]string{"encrypt": "false", "trustservercertificate": "true", "database": "entidad"})},
		{"sqlserver", "require", "", urlParams(map[string]string{"encrypt": "true", "trustservercertificate": "true"})},
		{"sqlserver", "verify", ca, urlParams(map[string]string{"encrypt": "true", "trustservercertificate": "false", "certificate": ca})},
		{"oracle", "disable", "", urlParams(map[string]string{"SSL": ""})},
		{"oracle", "require", "", urlParams(map[string]string{"SSL": "enable", "SSL VERIFY": "false"})},
		{"oracle", "verify", "", urlParams(map[string]string{"SSL": "enable", "SSL VERIFY": "true"})},
	}
	for _, c := range cases {
		t.Run(c.engine+"/"+c.tls+"/"+filepath.Base(c.ca), func(t *testing.T) {
			d, _ := For(c.engine)
			cfg := db(c.engine, c.tls)
			cfg.CAFile = c.ca
			dsn, err := d.DSN(cfg, hardPassword)
			if err != nil {
				t.Fatal(err)
			}
			c.check(t, dsn)
		})
	}
	for _, tls := range []string{"disable", "require", "verify", "verify+ca"} {
		t.Run("mysql/"+tls, func(t *testing.T) {
			d, _ := For("mysql")
			cfg := db("mysql", strings.TrimSuffix(tls, "+ca"))
			if strings.HasSuffix(tls, "+ca") {
				cfg.CAFile = ca
			}
			dsn, err := d.DSN(cfg, hardPassword)
			if err != nil {
				t.Fatal(err)
			}
			mc, err := mysql.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if mc.Passwd != hardPassword || mc.User != "rid_reader" || mc.DBName != "entidad" || !mc.ParseTime || mc.Addr != "bd.interna:1234" {
				t.Fatalf("%+v", mc)
			}
			want := map[string]string{"disable": "false", "require": "skip-verify", "verify": "true"}[tls]
			if want == "" {
				want = "rid-" // a registered config with the CA
			}
			if !strings.HasPrefix(mc.TLSConfig, want) {
				t.Fatalf("tls=%s: TLSConfig %q", tls, mc.TLSConfig)
			}
		})
	}
	d, _ := For("mysql")
	cfg := db("mysql", "verify")
	cfg.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := d.DSN(cfg, "x"); err == nil {
		t.Fatal("a missing CA file must be an error")
	}
}

func TestQuote(t *testing.T) {
	cases := map[string][2]string{
		"sqlserver": {`a]b`, `[a]]b]`},
		"postgres":  {`a"b`, `"a""b"`},
		"oracle":    {`a"b`, `"a""b"`},
		"mysql":     {"a`b", "`a``b`"},
	}
	for e, c := range cases {
		d, _ := For(e)
		if got := d.Quote(c[0]); got != c[1] {
			t.Errorf("%s: %s", e, got)
		}
	}
}

func TestKinds(t *testing.T) {
	scale0 := sql.NullInt64{Int64: 0, Valid: true}
	cases := []struct {
		engine, native string
		scale          sql.NullInt64
		want           instant.Kind
	}{
		{"sqlserver", "nvarchar", sql.NullInt64{}, instant.KindString},
		{"sqlserver", "bit", sql.NullInt64{}, instant.KindBoolean},
		{"sqlserver", "datetimeoffset", sql.NullInt64{}, instant.KindDateTimeTZ},
		{"sqlserver", "uniqueidentifier", sql.NullInt64{}, instant.KindUUID},
		{"sqlserver", "varbinary", sql.NullInt64{}, instant.KindUnsupported},
		{"postgres", "numeric", sql.NullInt64{}, instant.KindNumber},
		{"postgres", "timestamp without time zone", sql.NullInt64{}, instant.KindDateTime},
		{"postgres", "bytea", sql.NullInt64{}, instant.KindUnsupported},
		{"mysql", "tinyint", sql.NullInt64{}, instant.KindInteger},
		{"mysql", "bit", sql.NullInt64{}, instant.KindBoolean},
		{"mysql", "blob", sql.NullInt64{}, instant.KindUnsupported},
		{"oracle", "NUMBER", scale0, instant.KindInteger},
		{"oracle", "NUMBER", sql.NullInt64{}, instant.KindNumber},
		{"oracle", "DATE", sql.NullInt64{}, instant.KindDateTime},
		{"oracle", "TIMESTAMP(6) WITH TIME ZONE", sql.NullInt64{}, instant.KindDateTimeTZ},
		{"oracle", "TIMESTAMP(6) WITH LOCAL TIME ZONE", sql.NullInt64{}, instant.KindDateTime},
		{"oracle", "BLOB", sql.NullInt64{}, instant.KindUnsupported},
	}
	for _, c := range cases {
		d, _ := For(c.engine)
		if got := d.Kind(c.native, c.scale); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.engine, c.native, got, c.want)
		}
	}
}

func TestSelectByKey(t *testing.T) {
	want := map[string]string{
		"sqlserver": "SELECT TOP (2) [a], [b] FROM [s].[v] WHERE [k] = @p1",
		"postgres":  `SELECT "a", "b" FROM "s"."v" WHERE "k" = $1 LIMIT 2`,
		"mysql":     "SELECT `a`, `b` FROM `s`.`v` WHERE `k` = ? LIMIT 2",
		"oracle":    `SELECT "a", "b" FROM "s"."v" WHERE "k" = :1 FETCH FIRST 2 ROWS ONLY`,
	}
	for e, w := range want {
		d, _ := For(e)
		got := d.SelectByKey([]string{d.Quote("a"), d.Quote("b")}, d.Quote("s")+"."+d.Quote("v"), d.Quote("k"), 2)
		if got != w {
			t.Errorf("%s:\n got %s\nwant %s", e, got, w)
		}
	}
}

// Final review I4: Oracle compares CHAR blank-padded only against CHAR; a VARCHAR2 bind of "ABC"
// never equals a CHAR(10) "ABC       ". The Oracle dialect pads fixed-width keys.
func TestOracleBindsCharKeysPadded(t *testing.T) {
	d, _ := For("oracle")
	b, ok := d.(instant.KeyBinder)
	if !ok {
		t.Fatal("the Oracle dialect must bind keys")
	}
	if got := b.BindKey(instant.Column{Native: "CHAR", MaxLength: 10}, "ABC"); got != "ABC       " {
		t.Fatalf("%q", got)
	}
	if got := b.BindKey(instant.Column{Native: "NCHAR", MaxLength: 5}, "ñandú"); got != "ñandú" {
		t.Fatalf("already full: %q", got)
	}
	if got := b.BindKey(instant.Column{Native: "VARCHAR2", MaxLength: 10}, "ABC"); got != "ABC" {
		t.Fatalf("VARCHAR2 keys are bound as they come: %q", got)
	}
	for _, e := range []string{"postgres", "mysql", "sqlserver"} {
		if d, _ := For(e); func() bool { _, ok := d.(instant.KeyBinder); return ok }() {
			t.Fatalf("%s compares CHAR without padding the bind: it must not pad", e)
		}
	}
}
