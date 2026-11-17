//go:build !canal

package dialect

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
)

type mysqlDialect struct{}

func (mysqlDialect) Driver() string { return "mysql" }

func (mysqlDialect) DSN(db config.Database, password string) (string, error) {
	c := mysql.NewConfig()
	c.User, c.Passwd = db.User, password
	c.Net, c.Addr, c.DBName = "tcp", net.JoinHostPort(db.Host, strconv.Itoa(db.Port)), db.Name
	c.ParseTime, c.Loc = true, time.UTC
	c.Timeout = time.Duration(timeoutSeconds(db.QueryTimeout)) * time.Second
	switch db.TLS {
	case "disable":
		c.TLSConfig = "false"
	case "require":
		c.TLSConfig = "skip-verify"
	case "verify":
		c.TLSConfig = "true" // the system's roots
		if db.CAFile != "" {
			name, err := registerCA(db.Host, db.CAFile)
			if err != nil {
				return "", err
			}
			c.TLSConfig = name
		}
	}
	return c.FormatDSN(), nil
}

// registerCA registers a TLS configuration with the entity's CA under a name the DSN refers to.
func registerCA(host, caFile string) (string, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la CA de la base de datos %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return "", fmt.Errorf("%s no contiene certificados PEM", caFile)
	}
	sum := sha256.Sum256(append([]byte(host+"\x00"), pem...))
	name := "rid-" + hex.EncodeToString(sum[:8])
	if err := mysql.RegisterTLSConfig(name, &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
		return "", err
	}
	return name, nil
}

// Objects: information_schema in MySQL only shows objects the user holds some privilege on.
func (mysqlDialect) Objects() string {
	return `SELECT table_schema, table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END
FROM information_schema.tables
WHERE table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY 1, 2`
}

func (mysqlDialect) Columns() string {
	return `SELECT column_name, data_type, character_maximum_length, numeric_scale, is_nullable
FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`
}

func (mysqlDialect) Quote(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func (mysqlDialect) SelectByKey(cols []string, from, key string, limit int) string {
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = ? LIMIT %d", strings.Join(cols, ", "), from, key, limit)
}

// Kind: TINYINT is an integer (as: boolean exposes the usual TINYINT(1) flags).
func (mysqlDialect) Kind(t string, _ sql.NullInt64) instant.Kind {
	switch lower(t) {
	case "varchar", "char", "text", "tinytext", "mediumtext", "longtext", "enum":
		return instant.KindString
	case "int", "smallint", "tinyint", "mediumint", "bigint", "year":
		return instant.KindInteger
	case "bit":
		return instant.KindBoolean
	case "decimal", "float", "double":
		return instant.KindNumber
	case "date":
		return instant.KindDate
	case "datetime", "timestamp":
		return instant.KindDateTime
	}
	return instant.KindUnsupported
}

func (mysqlDialect) BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return beginReadOnly(ctx, db) // START TRANSACTION READ ONLY
}

// WritePrivileges counts INSERT, UPDATE and DELETE granted to the user globally, on the schema, on
// the object or on one of its columns, plus what the roles enabled in the session grant on the
// object or its columns (MySQL 8: information_schema.role_*_grants, privileges as a set such as
// "Select,Insert"). Wildcard schema grants (`app%`.*) are not resolved.
func (mysqlDialect) WritePrivileges() string {
	return `SELECT
  (SELECT COUNT(DISTINCT p.privilege_type) FROM (
     SELECT privilege_type, NULL AS table_schema, NULL AS table_name, grantee FROM information_schema.user_privileges
     UNION ALL SELECT privilege_type, table_schema, NULL, grantee FROM information_schema.schema_privileges
     UNION ALL SELECT privilege_type, table_schema, table_name, grantee FROM information_schema.table_privileges
     UNION ALL SELECT privilege_type, table_schema, table_name, grantee FROM information_schema.column_privileges
   ) p
   WHERE (p.table_schema IS NULL OR p.table_schema = a.s) AND (p.table_name IS NULL OR p.table_name = a.n)
     AND p.privilege_type IN ('INSERT', 'UPDATE', 'DELETE')
     AND p.grantee = CONCAT('''', SUBSTRING_INDEX(CURRENT_USER(), '@', 1), '''@''', SUBSTRING_INDEX(CURRENT_USER(), '@', -1), ''''))
  + (SELECT COUNT(*) FROM (
       SELECT table_schema, table_name, privilege_type FROM information_schema.role_table_grants
       UNION ALL SELECT table_schema, table_name, privilege_type FROM information_schema.role_column_grants
     ) r
     WHERE r.table_schema = a.s AND r.table_name = a.n
       AND (FIND_IN_SET('Insert', r.privilege_type) OR FIND_IN_SET('Update', r.privilege_type) OR FIND_IN_SET('Delete', r.privilege_type)))
FROM (SELECT ? AS s, ? AS n) a`
}
