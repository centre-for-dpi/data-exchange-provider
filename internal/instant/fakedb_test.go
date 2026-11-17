//go:build !canal

package instant

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

// fakeDB is an in-memory database behind a database/sql driver, one per DSN (= database name).
type fakeDB struct {
	mu        sync.Mutex
	down      bool  // connections and queries fail as if the server were unreachable
	queryErr  error // every query by key fails with this error (a driver message)
	objects   [][]driver.Value
	columns   map[string][][]driver.Value // "schema.name" → (name, type, maxLength, scale, nullable)
	rows      map[string][][]driver.Value // key → rows of the selected columns
	writes    int64                       // what WritePrivileges answers
	readOnly  int                         // read-only transactions begun
	byKey     int                         // queries by key run
	catalogue int                         // catalog reads
}

var (
	fakesMu sync.Mutex
	fakes   = map[string]*fakeDB{}
)

func init() { sql.Register("ridfake", fakeDriver{}) }

func newFakeDB(name string) *fakeDB {
	f := &fakeDB{columns: map[string][][]driver.Value{}, rows: map[string][][]driver.Value{}}
	fakesMu.Lock()
	fakes[name] = f
	fakesMu.Unlock()
	return f
}

func (f *fakeDB) set(fn func(f *fakeDB)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

var errRefused = &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

type fakeDriver struct{}

func (fakeDriver) Open(name string) (driver.Conn, error) {
	fakesMu.Lock()
	f := fakes[name]
	fakesMu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("no fake database %q", name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errRefused
	}
	return &fakeConn{f: f}, nil
}

type fakeConn struct{ f *fakeDB }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }
func (c *fakeConn) BeginTx(_ context.Context, o driver.TxOptions) (driver.Tx, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	if c.f.down {
		return nil, driver.ErrBadConn
	}
	if o.ReadOnly {
		c.f.readOnly++
	}
	return fakeTx{}, nil
}
func (c *fakeConn) Ping(context.Context) error {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	if c.f.down {
		return driver.ErrBadConn
	}
	return nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	f := c.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, driver.ErrBadConn
	}
	switch {
	case query == "OBJECTS":
		f.catalogue++
		return &fakeRows{cols: []string{"schema", "name", "kind"}, rows: f.objects}, nil
	case query == "COLUMNS":
		return &fakeRows{cols: []string{"name", "type", "len", "scale", "nullable"}, rows: f.columns[fmt.Sprint(args[0].Value)+"."+fmt.Sprint(args[1].Value)]}, nil
	case query == "WRITES":
		return &fakeRows{cols: []string{"n"}, rows: [][]driver.Value{{f.writes}}}, nil
	case strings.HasPrefix(query, "SELECT "):
		f.byKey++
		if f.queryErr != nil {
			return nil, f.queryErr
		}
		// rows are stored with every column of the (single) object, in catalog order: project them.
		selected := strings.Split(strings.SplitN(strings.TrimPrefix(query, "SELECT "), " FROM ", 2)[0], ", ")
		var all [][]driver.Value
		for _, cols := range f.columns {
			all = cols
		}
		var out [][]driver.Value
		for _, row := range f.rows[fmt.Sprint(args[0].Value)] {
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
