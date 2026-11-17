//go:build !canal

// Package instant serves instant APIs: GET by key over the views and tables a read-only
// database user can SELECT, with an OpenAPI generated from the same declaration.
package instant

import (
	"context"
	"database/sql"
	"strings"

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
	// SelectByKey reads at most limit rows by key; orderBy (the selected columns, for many) makes
	// the list the same on every call.
	SelectByKey(cols []string, from, key string, orderBy []string, limit int) string
	Kind(native string, scale sql.NullInt64) Kind
	// BeginReadOnly starts the transaction every query runs in (READ ONLY where the engine has it).
	BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error)
	// WritePrivileges takes (schema, name) and returns how many of INSERT, UPDATE and DELETE the
	// user holds on it.
	WritePrivileges() string
}

// ReadCatalog lists the tables and views the connected user can SELECT, with their columns. With
// sources, it reads the columns only of the objects that match them (without case), so compiling
// costs a few round trips however broad the user's grants are; nil reads every object.
func ReadCatalog(ctx context.Context, db *sql.DB, d Dialect, sources []string) ([]Object, error) {
	rows, err := db.QueryContext(ctx, d.Objects())
	if f, ok := d.(ObjectsFallbacker); ok && err != nil && errorClass(err) == "query" {
		rows, err = db.QueryContext(ctx, f.ObjectsFallback())
	}
	if err != nil {
		return nil, err
	}
	var objs []Object
	for rows.Next() {
		var o Object
		if err := rows.Scan(&o.Schema, &o.Name, &o.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		if sources == nil || matchesAny(o, sources) {
			objs = append(objs, o)
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	for i := range objs {
		cols, err := db.QueryContext(ctx, d.Columns(), objs[i].Schema, objs[i].Name)
		if err != nil {
			return nil, err
		}
		for cols.Next() {
			var c Column
			var maxLen, scale sql.NullInt64
			var nullable string
			if err := cols.Scan(&c.Name, &c.Native, &maxLen, &scale, &nullable); err != nil {
				cols.Close()
				return nil, err
			}
			c.MaxLength = int(maxLen.Int64)
			switch strings.ToUpper(strings.TrimSpace(nullable)) {
			case "Y", "YES", "1", "TRUE":
				c.Nullable = true
			}
			c.Kind = d.Kind(c.Native, scale)
			objs[i].Columns = append(objs[i].Columns, c)
		}
		if err := closeRows(cols); err != nil {
			return nil, err
		}
	}
	return objs, nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

func matchesAny(o Object, sources []string) bool {
	for _, src := range sources {
		if strings.EqualFold(o.Schema+"."+o.Name, src) {
			return true
		}
	}
	return false
}

// KeyBinder is implemented by dialects that must adapt the key before binding it (Oracle compares
// a CHAR column blank-padded only against another CHAR, so its fixed-width keys are padded).
type KeyBinder interface {
	BindKey(key Column, value string) any
}

// ObjectsFallbacker is implemented by dialects whose Objects query needs a newer server than some
// entities run (Oracle's oracle_maintained is 12c+): when Objects is rejected, ReadCatalog tries
// ObjectsFallback.
type ObjectsFallbacker interface {
	ObjectsFallback() string
}

// ServerVersioner gives the query that returns the database server's version (one row, one
// column), for catalog and doctor.
type ServerVersioner interface {
	ServerVersion() string
}
