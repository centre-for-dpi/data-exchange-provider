//go:build !canal

// Package instant serves instant APIs: GET by key over the views and tables a read-only
// database user can SELECT, with an OpenAPI generated from the same declaration.
package instant

import (
	"context"
	"database/sql"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

// Column is a column as the catalog describes it.
type Column struct {
	Name      string // as the catalog spells it (Oracle: upper case)
	Native    string // the engine's type name, for messages
	Kind      Kind
	MaxLength int
	Nullable  bool
}

// Object is a table or view the user can SELECT.
type Object struct {
	Schema, Name, Kind string // Kind: "table" | "view"
	Columns            []Column
}

// Dialect is what changes between engines. Implementations live in package dialect.
type Dialect interface {
	Driver() string
	// DSN builds the connection string; TLS is disable, require or verify (with an optional CA).
	DSN(db config.Database, password string) (string, error)
	// Objects returns (schema, name, kind) for the tables and views the user can SELECT.
	Objects() string
	// Columns takes (schema, name) and returns (name, type, maxLength, scale, nullable).
	Columns() string
	Quote(ident string) string
	SelectByKey(cols []string, from, key string, limit int) string
	Kind(native string, scale sql.NullInt64) Kind
	// BeginReadOnly starts the transaction every query runs in (READ ONLY where the engine has it).
	BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error)
	// WritePrivileges takes (schema, name) and returns how many of INSERT, UPDATE and DELETE the
	// user holds on it.
	WritePrivileges() string
}
