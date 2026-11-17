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
	"unicode/utf8"

	_ "github.com/sijms/go-ora/v2" // driver "oracle", pure Go (no Instant Client)

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
)

type oracle struct{}

func (oracle) Driver() string { return "oracle" }

// DSN: built here, not with go_ora.BuildUrl, which does not escape @ or : in the password.
// tls: verify uses the system's roots (go-ora takes no CA file; config rejects caFile).
func (oracle) DSN(db config.Database, password string) (string, error) {
	q := url.Values{}
	q.Set("TIMEOUT", strconv.Itoa(timeoutSeconds(db.QueryTimeout)))
	switch db.TLS {
	case "require":
		q.Set("SSL", "enable")
		q.Set("SSL VERIFY", "false")
	case "verify":
		q.Set("SSL", "enable")
		q.Set("SSL VERIFY", "true")
	}
	u := url.URL{Scheme: "oracle", User: url.UserPassword(db.User, password),
		Host: net.JoinHostPort(db.Host, strconv.Itoa(db.Port)), Path: "/" + db.Name, RawQuery: q.Encode()}
	return u.String(), nil
}

// Objects: the ALL_* views only show what the user has privileges on; Oracle's own schemas are
// skipped.
func (oracle) Objects() string {
	return `SELECT owner, table_name, 'table' FROM all_tables
WHERE owner IN (SELECT username FROM all_users WHERE oracle_maintained = 'N')
UNION ALL
SELECT owner, view_name, 'view' FROM all_views
WHERE owner IN (SELECT username FROM all_users WHERE oracle_maintained = 'N')
ORDER BY 1, 2`
}

func (oracle) Columns() string {
	return `SELECT column_name, data_type, char_length, data_scale, nullable
FROM all_tab_columns WHERE owner = :1 AND table_name = :2 ORDER BY column_id`
}

func (oracle) Quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func (oracle) SelectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = :1 FETCH FIRST %d ROWS ONLY", strings.Join(cols, ", "), from, key, limit)
}

func (oracle) Kind(t string, scale sql.NullInt64) instant.Kind {
	t = lower(t)
	switch {
	case t == "varchar2" || t == "nvarchar2" || t == "char" || t == "nchar":
		return instant.KindString
	case t == "number":
		if scale.Valid && scale.Int64 == 0 {
			return instant.KindInteger
		}
		return instant.KindNumber
	case t == "float" || t == "binary_float" || t == "binary_double":
		return instant.KindNumber
	case t == "boolean":
		return instant.KindBoolean
	case t == "date": // Oracle DATE carries a time of day
		return instant.KindDateTime
	case strings.HasPrefix(t, "timestamp") && strings.HasSuffix(t, " with time zone") && !strings.Contains(t, "local"):
		return instant.KindDateTimeTZ
	case strings.HasPrefix(t, "timestamp"):
		return instant.KindDateTime
	}
	return instant.KindUnsupported
}

// BeginReadOnly: go-ora refuses TxOptions.ReadOnly, so the transaction's first statement makes
// it read-only.
func (oracle) BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// WritePrivileges: object grants to the user, PUBLIC or an enabled role, the ANY TABLE system
// privileges, and ownership (an owner can always write).
func (oracle) WritePrivileges() string {
	return `SELECT
  (SELECT COUNT(DISTINCT privilege) FROM all_tab_privs p
    WHERE p.table_schema = a.s AND p.table_name = a.n AND p.privilege IN ('INSERT', 'UPDATE', 'DELETE')
      AND (p.grantee = USER OR p.grantee = 'PUBLIC' OR p.grantee IN (SELECT role FROM session_roles)))
  + (SELECT COUNT(*) FROM session_privs WHERE privilege IN ('INSERT ANY TABLE', 'UPDATE ANY TABLE', 'DELETE ANY TABLE'))
  + CASE WHEN a.s = USER THEN 3 ELSE 0 END
FROM (SELECT :1 AS s, :2 AS n FROM dual) a`
}

// BindKey pads CHAR and NCHAR keys to the column's length: Oracle compares blank-padded only when
// both sides are CHAR, and go-ora binds a string as VARCHAR2, so "ABC" would never match a
// CHAR(10) "ABC       ".
func (oracle) BindKey(c instant.Column, value string) any {
	switch lower(c.Native) {
	case "char", "nchar":
		if n := utf8.RuneCountInString(value); c.MaxLength > n {
			return value + strings.Repeat(" ", c.MaxLength-n)
		}
	}
	return value
}
