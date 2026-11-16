// THROWAWAY SPIKE. One small dialect per engine: how to list what the user can read, how to quote
// identifiers, how to select by key with a row limit, and how to map native types.
package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/microsoft/go-mssqldb/namedpipe"
	_ "github.com/sijms/go-ora/v2"
)

type dialect interface {
	driver() string
	// objectsQuery returns (schema, name, kind) for the tables and views the user can SELECT.
	objectsQuery() string
	// columnsQuery takes (schema, name) and returns (name, type, maxLength, scale, nullable).
	columnsQuery() string
	quote(ident string) string
	selectByKey(columns []string, from, key string, limit int) string
	kind(nativeType string, scale sql.NullInt64) string
}

func dialectFor(engine string) (dialect, error) {
	switch engine {
	case "sqlserver":
		return sqlServer{}, nil
	case "postgres":
		return postgres{}, nil
	case "mysql":
		return mysql{}, nil
	case "oracle":
		return oracle{}, nil
	}
	return nil, fmt.Errorf("unknown engine %q", engine)
}

func lower(t string) string { return strings.ToLower(strings.TrimSpace(t)) }

// ---------- SQL Server ----------

type sqlServer struct{}

func (sqlServer) driver() string { return "sqlserver" }
func (sqlServer) objectsQuery() string {
	return `SELECT s.name, o.name, CASE o.type WHEN 'V' THEN 'view' ELSE 'table' END
FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id
WHERE o.type IN ('U','V') AND o.is_ms_shipped = 0
  AND HAS_PERMS_BY_NAME(QUOTENAME(s.name) + '.' + QUOTENAME(o.name), 'OBJECT', 'SELECT') = 1
ORDER BY s.name, o.name`
}
func (sqlServer) columnsQuery() string {
	return `SELECT c.name, t.name,
  CASE WHEN t.name IN ('nvarchar','nchar') THEN c.max_length / 2 ELSE c.max_length END, c.scale,
  CASE c.is_nullable WHEN 1 THEN 'YES' ELSE 'NO' END
FROM sys.columns c JOIN sys.types t ON t.user_type_id = c.user_type_id
WHERE c.object_id = OBJECT_ID(QUOTENAME(@p1) + '.' + QUOTENAME(@p2)) ORDER BY c.column_id`
}
func (sqlServer) quote(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }
func (sqlServer) selectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT TOP (%d) %s FROM %s WHERE %s = @p1", limit, strings.Join(cols, ", "), from, key)
}
func (sqlServer) kind(t string, _ sql.NullInt64) string {
	switch lower(t) {
	case "varchar", "nvarchar", "char", "nchar":
		return "string"
	case "int", "smallint", "tinyint", "bigint":
		return "int"
	case "bit":
		return "bool"
	case "decimal", "numeric", "money", "smallmoney":
		return "decimal"
	case "date":
		return "date"
	case "datetimeoffset":
		return "timestamptz"
	case "datetime2", "datetime", "smalldatetime":
		return "timestamp"
	case "uniqueidentifier":
		return "uuid"
	}
	return ""
}

// ---------- PostgreSQL ----------

type postgres struct{}

func (postgres) driver() string { return "pgx" }
func (postgres) objectsQuery() string {
	return `SELECT table_schema, table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND has_table_privilege(quote_ident(table_schema) || '.' || quote_ident(table_name), 'SELECT')
ORDER BY 1, 2`
}
func (postgres) columnsQuery() string {
	return `SELECT column_name, data_type, character_maximum_length, numeric_scale, is_nullable
FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`
}
func (postgres) quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (postgres) selectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1 LIMIT %d", strings.Join(cols, ", "), from, key, limit)
}
func (postgres) kind(t string, _ sql.NullInt64) string {
	switch lower(t) {
	case "character varying", "character", "text":
		return "string"
	case "integer", "smallint", "bigint":
		return "int"
	case "boolean":
		return "bool"
	case "numeric":
		return "decimal"
	case "date":
		return "date"
	case "timestamp with time zone":
		return "timestamptz"
	case "timestamp without time zone":
		return "timestamp"
	case "uuid":
		return "uuid"
	}
	return ""
}

// ---------- MySQL ----------

type mysql struct{}

func (mysql) driver() string { return "mysql" }

// information_schema in MySQL only shows objects the user has some privilege on.
func (mysql) objectsQuery() string {
	return `SELECT table_schema, table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
FROM information_schema.tables
WHERE table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY 1, 2`
}
func (mysql) columnsQuery() string {
	return `SELECT column_name, data_type, character_maximum_length, numeric_scale, is_nullable
FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`
}
func (mysql) quote(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func (mysql) selectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = ? LIMIT %d", strings.Join(cols, ", "), from, key, limit)
}
func (mysql) kind(t string, _ sql.NullInt64) string {
	switch lower(t) {
	case "varchar", "char", "text":
		return "string"
	case "int", "smallint", "bigint", "mediumint":
		return "int"
	case "tinyint", "bit":
		return "bool"
	case "decimal":
		return "decimal"
	case "date":
		return "date"
	case "datetime", "timestamp":
		return "timestamp"
	}
	return ""
}

// ---------- Oracle (go-ora, pure Go, no Instant Client) ----------

type oracle struct{}

func (oracle) driver() string { return "oracle" }

// ALL_* views only show what the user has privileges on; Oracle-maintained schemas are skipped.
func (oracle) objectsQuery() string {
	return `SELECT owner, table_name, 'table' FROM all_tables
WHERE owner IN (SELECT username FROM all_users WHERE oracle_maintained = 'N')
UNION ALL
SELECT owner, view_name, 'view' FROM all_views
WHERE owner IN (SELECT username FROM all_users WHERE oracle_maintained = 'N')
ORDER BY 1, 2`
}
func (oracle) columnsQuery() string {
	return `SELECT column_name, data_type, char_length, data_scale, nullable
FROM all_tab_columns WHERE owner = :1 AND table_name = :2 ORDER BY column_id`
}
func (oracle) quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (oracle) selectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = :1 FETCH FIRST %d ROWS ONLY", strings.Join(cols, ", "), from, key, limit)
}
func (oracle) kind(t string, scale sql.NullInt64) string {
	t = lower(t)
	switch {
	case t == "varchar2" || t == "nvarchar2" || t == "char" || t == "nchar":
		return "string"
	case t == "number":
		if scale.Valid && scale.Int64 == 0 {
			return "int"
		}
		return "decimal"
	case t == "boolean":
		return "bool"
	case t == "date": // Oracle DATE carries a time of day
		return "timestamp"
	case strings.HasPrefix(t, "timestamp") && strings.HasSuffix(t, "with time zone"):
		return "timestamptz"
	case strings.HasPrefix(t, "timestamp"):
		return "timestamp"
	}
	return ""
}
