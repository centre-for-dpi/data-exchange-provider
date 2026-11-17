//go:build !canal

package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
)

type postgres struct{}

func (postgres) Driver() string { return "pgx" }

func (postgres) DSN(db config.Database, password string) (string, error) {
	q := url.Values{}
	q.Set("application_name", "den-node")
	q.Set("connect_timeout", strconv.Itoa(timeoutSeconds(db.QueryTimeout)))
	switch db.TLS {
	case "disable":
		q.Set("sslmode", "disable")
	case "require":
		q.Set("sslmode", "require")
	case "verify":
		q.Set("sslmode", "verify-full") // without sslrootcert: the system's roots
		if db.CAFile != "" {
			q.Set("sslrootcert", db.CAFile)
		}
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(db.User, password),
		Host: net.JoinHostPort(db.Host, strconv.Itoa(db.Port)), Path: "/" + db.Name, RawQuery: q.Encode()}
	return u.String(), nil
}

func (postgres) Objects() string {
	return `SELECT table_schema, table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND has_table_privilege(quote_ident(table_schema) || '.' || quote_ident(table_name), 'SELECT')
ORDER BY 1, 2`
}

func (postgres) Columns() string {
	return `SELECT column_name, data_type, character_maximum_length, numeric_scale, is_nullable
FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`
}

func (postgres) Quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func (d postgres) SelectByKey(cols []string, from string, key instant.Column, orderBy []string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1%s LIMIT %d", strings.Join(cols, ", "), from, d.Quote(key.Name), orderClause(orderBy), limit)
}

func (postgres) Kind(t string, _ sql.NullInt64) instant.Kind {
	switch lower(t) {
	case "character varying", "character", "text":
		return instant.KindString
	case "integer", "smallint", "bigint":
		return instant.KindInteger
	case "boolean":
		return instant.KindBoolean
	case "numeric", "real", "double precision":
		return instant.KindNumber
	case "date":
		return instant.KindDate
	case "timestamp with time zone":
		return instant.KindDateTimeTZ
	case "timestamp without time zone":
		return instant.KindDateTime
	case "uuid":
		return instant.KindUUID
	}
	return instant.KindUnsupported
}

func (postgres) BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return beginReadOnly(ctx, db) // BEGIN READ ONLY
}

func (postgres) WritePrivileges() string {
	// Column-level INSERT and UPDATE grants count too (has_any_column_privilege).
	return `SELECT (has_table_privilege(o, 'INSERT') OR has_any_column_privilege(o, 'INSERT'))::int
  + (has_table_privilege(o, 'UPDATE') OR has_any_column_privilege(o, 'UPDATE'))::int
  + has_table_privilege(o, 'DELETE')::int
FROM (SELECT quote_ident($1) || '.' || quote_ident($2) AS o) x`
}

func (postgres) ServerVersion() string {
	return `SELECT 'PostgreSQL ' || current_setting('server_version')`
}
