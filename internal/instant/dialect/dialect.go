//go:build !canal

// Package dialect holds what changes between database engines for the instant APIs: the
// connection string, the catalog queries, quoting, the query by key, type mapping, the
// read-only transaction and the write-privilege check. It links the four drivers.
package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
)

// For returns the dialect of an engine named in the configuration.
func For(engine string) (instant.Dialect, error) {
	switch engine {
	case "sqlserver":
		return sqlServer{}, nil
	case "postgres":
		return postgres{}, nil
	case "mysql":
		return mysqlDialect{}, nil
	case "oracle":
		return oracle{}, nil
	}
	return nil, fmt.Errorf("el motor %q no está soportado", engine)
}

func lower(t string) string { return strings.ToLower(strings.TrimSpace(t)) }

// orderClause is " ORDER BY a, b", or nothing.
func orderClause(cols []string) string {
	if len(cols) == 0 {
		return ""
	}
	return " ORDER BY " + strings.Join(cols, ", ")
}

func timeoutSeconds(d interface{ Seconds() float64 }) int {
	s := int(d.Seconds())
	if s < 1 {
		return 1
	}
	return s
}

// beginReadOnly is a READ ONLY transaction where database/sql and the driver support it.
func beginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	return db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}
