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

	_ "github.com/microsoft/go-mssqldb" // driver "sqlserver"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
)

type sqlServer struct{}

func (sqlServer) Driver() string { return "sqlserver" }

func (sqlServer) DSN(db config.Database, password string) (string, error) {
	q := url.Values{}
	q.Set("database", db.Name)
	q.Set("app name", "rid-node")
	q.Set("dial timeout", strconv.Itoa(timeoutSeconds(db.QueryTimeout)))
	switch db.TLS {
	case "disable": // "false": only the login is encrypted, which every SQL Server accepts
		q.Set("encrypt", "false")
	case "require":
		q.Set("encrypt", "true")
		q.Set("trustservercertificate", "true")
	case "verify":
		q.Set("encrypt", "true")
		q.Set("trustservercertificate", "false")
		if db.CAFile != "" {
			q.Set("certificate", db.CAFile)
		}
	}
	u := url.URL{Scheme: "sqlserver", User: url.UserPassword(db.User, password),
		Host: net.JoinHostPort(db.Host, strconv.Itoa(db.Port)), RawQuery: q.Encode()}
	return u.String(), nil
}

func (sqlServer) Objects() string {
	return `SELECT s.name, o.name, CASE o.type WHEN 'V' THEN 'view' ELSE 'table' END
FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE o.type IN ('U','V') AND o.is_ms_shipped = 0
  AND HAS_PERMS_BY_NAME(QUOTENAME(s.name) + '.' + QUOTENAME(o.name), 'OBJECT', 'SELECT') = 1
ORDER BY s.name, o.name`
}

func (sqlServer) Columns() string {
	return `SELECT c.name, t.name,
  CASE WHEN t.name IN ('nvarchar','nchar') AND c.max_length > 0 THEN c.max_length / 2 ELSE c.max_length END, c.scale,
  CASE c.is_nullable WHEN 1 THEN 'YES' ELSE 'NO' END
FROM sys.columns c JOIN sys.types t ON t.user_type_id = c.user_type_id
WHERE c.object_id = OBJECT_ID(QUOTENAME(@p1) + '.' + QUOTENAME(@p2)) ORDER BY c.column_id`
}

func (sqlServer) Quote(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }

func (sqlServer) SelectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT TOP (%d) %s FROM %s WHERE %s = @p1", limit, strings.Join(cols, ", "), from, key)
}

func (sqlServer) Kind(t string, _ sql.NullInt64) instant.Kind {
	switch lower(t) {
	case "varchar", "nvarchar", "char", "nchar":
		return instant.KindString
	case "int", "smallint", "tinyint", "bigint":
		return instant.KindInteger
	case "bit":
		return instant.KindBoolean
	case "decimal", "numeric", "money", "smallmoney", "float", "real":
		return instant.KindNumber
	case "date":
		return instant.KindDate
	case "datetimeoffset":
		return instant.KindDateTimeTZ
	case "datetime2", "datetime", "smalldatetime":
		return instant.KindDateTime
	case "uniqueidentifier":
		return instant.KindUUID
	}
	return instant.KindUnsupported
}

// BeginReadOnly: SQL Server has no read-only transaction; the read-only user and the SELECT-only
// queries are the guard.
func (sqlServer) BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, nil)
}

func (sqlServer) WritePrivileges() string {
	return `SELECT HAS_PERMS_BY_NAME(o, 'OBJECT', 'INSERT') + HAS_PERMS_BY_NAME(o, 'OBJECT', 'UPDATE') + HAS_PERMS_BY_NAME(o, 'OBJECT', 'DELETE')
FROM (SELECT QUOTENAME(@p1) + '.' + QUOTENAME(@p2) AS o) x`
}
