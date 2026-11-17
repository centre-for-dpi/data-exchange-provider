//go:build !canal

// Package instanttest is an in-memory database behind a database/sql driver ("denfake"), for
// tests of the instant APIs and the commands that use them. Each DSN (the database name) is
// one DB.
package instanttest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
)

// DB is one fake database. Change it with Set; read the counters under Mu.
type DB struct {
	Mu          sync.Mutex
	Down        bool  // connections and queries fail as if the server were unreachable
	QueryErr    error // every query by key fails with this error (a driver message)
	Objects     [][]driver.Value
	Columns     map[string][][]driver.Value // "schema.name" → (name, type, maxLength, scale, nullable)
	Rows        map[string][][]driver.Value // key → rows of the selected columns
	Writes      int64                       // what WritePrivileges answers
	ReadOnly    int                         // read-only transactions begun
	ByKey       int                         // queries by key run
	Catalogue   int
	ColumnReads int   // column queries run (one per object read)
	ProbeErr    error // a SELECT without parameters (the compile's permission probe) fails with this
	ReadOnlyErr error // beginning a read-only transaction fails with this                         // catalog reads
}

var (
	fakesMu sync.Mutex
	fakes   = map[string]*DB{}
)

// Driver is the database/sql driver name.
const Driver = "denfake"

func init() { sql.Register(Driver, fakeDriver{}) }

// New registers an empty database under name (the DSN that opens it).
func New(name string) *DB {
	f := &DB{Columns: map[string][][]driver.Value{}, Rows: map[string][][]driver.Value{}}
	fakesMu.Lock()
	fakes[name] = f
	fakesMu.Unlock()
	return f
}

// Set changes the database under its lock.
func (f *DB) Set(fn func(f *DB)) { f.Mu.Lock(); fn(f); f.Mu.Unlock() }

// ErrRefused is what dialing a down database returns.
var ErrRefused = &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

type fakeDriver struct{}

func (fakeDriver) Open(name string) (driver.Conn, error) {
	fakesMu.Lock()
	f := fakes[name]
	fakesMu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("no fake database %q", name)
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Down {
		return nil, ErrRefused
	}
	return &fakeConn{f: f}, nil
}

type fakeConn struct{ f *DB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }
func (c *fakeConn) BeginTx(_ context.Context, o driver.TxOptions) (driver.Tx, error) {
	c.f.Mu.Lock()
	defer c.f.Mu.Unlock()
	if c.f.Down {
		return nil, driver.ErrBadConn
	}
	if o.ReadOnly {
		if c.f.ReadOnlyErr != nil {
			return nil, c.f.ReadOnlyErr
		}
		c.f.ReadOnly++
	}
	return fakeTx{}, nil
}
func (c *fakeConn) Ping(context.Context) error {
	c.f.Mu.Lock()
	defer c.f.Mu.Unlock()
	if c.f.Down {
		return driver.ErrBadConn
	}
	return nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	f := c.f
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Down {
		return nil, driver.ErrBadConn
	}
	switch {
	case query == "OBJECTS":
		f.Catalogue++
		return &fakeRows{cols: []string{"schema", "name", "kind"}, rows: f.Objects}, nil
	case query == "COLUMNS":
		f.ColumnReads++
		return &fakeRows{cols: []string{"name", "type", "len", "scale", "nullable"}, rows: f.Columns[fmt.Sprint(args[0].Value)+"."+fmt.Sprint(args[1].Value)]}, nil
	case query == "VERSION":
		return &fakeRows{cols: []string{"v"}, rows: [][]driver.Value{{"FakeDB 1.0"}}}, nil
	case query == "WRITES":
		return &fakeRows{cols: []string{"n"}, rows: [][]driver.Value{{f.Writes}}}, nil
	case strings.HasPrefix(query, "SELECT ") && len(args) == 0:
		if f.ProbeErr != nil {
			return nil, f.ProbeErr
		}
		return &fakeRows{}, nil
	case strings.HasPrefix(query, "SELECT "):
		f.ByKey++
		if f.QueryErr != nil {
			return nil, f.QueryErr
		}
		// rows are stored with every column of the (single) object, in catalog order: project them.
		selected := strings.Split(strings.SplitN(strings.TrimPrefix(query, "SELECT "), " FROM ", 2)[0], ", ")
		var all [][]driver.Value
		for _, cols := range f.Columns {
			all = cols
		}
		var out [][]driver.Value
		for _, row := range f.Rows[fmt.Sprint(args[0].Value)] {
			var projected []driver.Value
			for _, name := range selected {
				for i, c := range all {
					if `"`+c[0].(string)+`"` == name {
						projected = append(projected, row[i])
					}
				}
			}
			out = append(out, projected)
		}
		return &fakeRows{cols: selected, rows: out}, nil
	}
	return nil, fmt.Errorf("unexpected query %q", query)
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}
