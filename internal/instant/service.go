//go:build !canal

package instant

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

// Service is one instant service: a pool to the entity's database and the resources compiled
// against its catalog. It serves 503 until the catalog compiles, and keeps retrying.
type Service struct {
	cfg config.Service
	d   Dialect
	db  *sql.DB
	log *slog.Logger

	set       atomic.Pointer[compiledSet]
	streak    atomic.Int32  // consecutive database errors while serving
	recompile chan struct{} // asks the loop to read the catalog again

	mu      sync.Mutex
	lastErr error // why the last compile failed (nil once it compiles)

	retryMin, retryMax time.Duration
	recompileEvery     time.Duration // the least time between two catalog reads asked by errors
	maxResponseBytes   int           // the values a response may carry; beyond it, 500 response_too_large
}

// DefaultMaxResponseBytes bounds one response (the container has 256 MiB for every service).
const DefaultMaxResponseBytes = 8 << 20

type compiledSet struct {
	list   []Compiled
	byName map[string]*Compiled
}

// recompileAfter is how many database errors in a row make the service read the catalog again
// (a view changed, a failover to a replica with another schema).
const recompileAfter = 3

// Open reads the password and prepares the pool. It does not connect: Start does, and retries.
func Open(svc config.Service, d Dialect, log *slog.Logger) (*Service, error) {
	db := svc.Instant.Database
	if db == nil {
		return nil, errors.New("el servicio no tiene database")
	}
	password, err := config.ReadSecret(db.PasswordFile)
	if err != nil {
		return nil, err
	}
	dsn, err := d.DSN(*db, password)
	if err != nil {
		return nil, err
	}
	pool, err := sql.Open(d.Driver(), dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(db.MaxConnections)
	pool.SetMaxIdleConns(db.MaxConnections)
	pool.SetConnMaxIdleTime(5 * time.Minute)
	pool.SetConnMaxLifetime(30 * time.Minute)
	return &Service{cfg: svc, d: d, db: pool, log: log.With("service", svc.Name), recompile: make(chan struct{}, 1),
		retryMin: time.Second, retryMax: 30 * time.Second, recompileEvery: 30 * time.Second, maxResponseBytes: DefaultMaxResponseBytes}, nil
}

// Close closes the pool.
func (s *Service) Close() error { return s.db.Close() }

// Start compiles in the background until it works, then again whenever serving errors pile up.
func (s *Service) Start(ctx context.Context) {
	go func() {
		wait := s.retryMin
		for {
			last := time.Now()
			err := s.Compile(ctx)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				s.log.Info("instant service ready", "resources", len(s.set.Load().list))
				wait = s.retryMin
				select {
				case <-ctx.Done():
					return
				case <-s.recompile:
				}
				// Errors never make the node scan the catalog more than once per recompileEvery.
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(last.Add(s.recompileEvery))):
				}
				continue
			}
			s.logError("instant service not ready", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(wait*2, s.retryMax)
		}
	}()
}

// Compile reads the catalog and compiles the resources. A database failure keeps what is being
// served; a declaration that no longer matches the catalog stops serving (all or nothing).
func (s *Service) Compile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, max(30*time.Second, 6*s.cfg.Instant.Database.QueryTimeout))
	defer cancel()
	sources := []string{}
	for _, r := range s.cfg.Instant.Resources {
		sources = append(sources, r.Source)
	}
	cat, err := ReadCatalog(ctx, s.db, s.d, sources)
	if err != nil {
		err = dbError(err)
		s.setErr(err)
		return err
	}
	rs, err := Compile(s.cfg, cat, s.d)
	if err == nil {
		err = s.probe(ctx, rs)
	}
	var de *DBError
	if errors.As(err, &de) {
		s.setErr(err) // the database failed: keep serving what compiled before
		return err
	}
	if err != nil {
		s.set.Store(nil)
		s.setErr(err)
		return err
	}
	set := &compiledSet{list: rs, byName: map[string]*Compiled{}}
	for i := range rs {
		set.byName[rs[i].Name] = &set.list[i]
	}
	s.set.Store(set)
	s.streak.Store(0)
	s.setErr(nil)
	return nil
}

func (s *Service) setErr(err error) { s.mu.Lock(); s.lastErr = err; s.mu.Unlock() }

// Err is why the service is not ready (nil when it is).
func (s *Service) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.lastErr }

// Ready reports whether the catalog compiled and the resources are served.
func (s *Service) Ready() bool { return s.set.Load() != nil }

// Catalog lists the tables and views the user can SELECT.
func (s *Service) Catalog(ctx context.Context) ([]Object, error) {
	cat, err := ReadCatalog(ctx, s.db, s.d, nil)
	if err != nil {
		return nil, dbError(err)
	}
	return cat, nil
}

// Probe is the service's health: compiled, and the database answers.
func (s *Service) Probe(ctx context.Context) error {
	if !s.Ready() {
		if err := s.Err(); err != nil {
			return err
		}
		return errors.New("el catálogo todavía no se ha leído")
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Instant.Database.QueryTimeout)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		return dbError(err)
	}
	return nil
}

// OpenAPI is the service's OpenAPI document, once the catalog compiled.
func (s *Service) OpenAPI() ([]byte, error) {
	set := s.set.Load()
	if set == nil {
		if err := s.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("el catálogo todavía no se ha leído")
	}
	return OpenAPI(s.cfg, set.list)
}

// WriteCheck returns the sources the database user could write to (INSERT, UPDATE or DELETE).
func (s *Service) WriteCheck(ctx context.Context) ([]string, error) {
	set := s.set.Load()
	if set == nil {
		return nil, s.Err()
	}
	var writable []string
	for _, r := range set.list {
		var n int64
		if err := s.db.QueryRowContext(ctx, s.d.WritePrivileges(), r.Object.Schema, r.Object.Name).Scan(&n); err != nil {
			return nil, dbError(err)
		}
		if n > 0 {
			writable = append(writable, r.Source)
		}
	}
	return writable, nil
}

// Handler serves the service's resources (the servicePath prefix is already stripped).
func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serve) }

// DBError is a database failure by class. Its message never carries the driver's text, which can
// hold values; Unwrap gives it to an operator's terminal (never to the logs).
type DBError struct {
	Class string // connection | timeout | canceled | query
	err   error
}

func (e *DBError) Error() string {
	switch e.Class {
	case "connection":
		return "no se pudo conectar con la base de datos"
	case "timeout":
		return "la base de datos no respondió a tiempo"
	case "canceled":
		return "la consulta se canceló"
	}
	return "la base de datos rechazó la consulta"
}

func (e *DBError) Unwrap() error { return e.err }

func dbError(err error) error {
	var de *DBError
	if errors.As(err, &de) {
		return err
	}
	return &DBError{Class: errorClass(err), err: err}
}

func errorClass(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, driver.ErrBadConn), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &ne):
		return "connection"
	}
	return "query"
}

// logError logs a failure without the driver's message: a database error by class, a
// declaration error (ours, without values) in full.
func (s *Service) logError(msg string, err error, attrs ...any) {
	var de *DBError
	if errors.As(err, &de) {
		s.log.Warn(msg, append(attrs, "class", de.Class)...)
		return
	}
	s.log.Warn(msg, append(attrs, "error", err.Error())...)
}

// served counts a request's database outcome; enough errors in a row ask for a recompile.
func (s *Service) served(err error) {
	if err == nil {
		s.streak.Store(0)
		return
	}
	// Only connection failures and timeouts point at the database (a failover, a restart);
	// query errors can come from a consumer's key and must not make the node scan the catalog.
	if c := errorClass(err); c != "connection" && c != "timeout" {
		return
	}
	if s.streak.Add(1) >= recompileAfter {
		s.streak.Store(0)
		select {
		case s.recompile <- struct{}{}:
		default:
		}
	}
}

// String is for logs and messages.
func (s *Service) String() string { return fmt.Sprintf("instant:%s", s.cfg.Name) }

// probe runs each resource's empty SELECT: the catalog of MySQL and Oracle lists objects the user
// holds any privilege on, so only a real SELECT tells whether it can read them.
func (s *Service) probe(ctx context.Context, rs []Compiled) error {
	var errs []error
	for _, r := range rs {
		rows, err := s.db.QueryContext(ctx, r.Probe)
		if err != nil {
			if c := errorClass(err); c == "connection" || c == "timeout" || c == "canceled" {
				return dbError(err)
			}
			errs = append(errs, fmt.Errorf("recurso %q: el usuario de la base no puede leer %s con SELECT (revisa sus permisos sobre la vista y sus columnas)", r.Name, r.Source))
			continue
		}
		_ = rows.Close()
	}
	return errors.Join(errs...)
}

// CheckReadOnly opens the read-only transaction every query runs in, and rolls it back.
func (s *Service) CheckReadOnly(ctx context.Context) error {
	tx, err := s.d.BeginReadOnly(ctx, s.db)
	if err != nil {
		return dbError(err)
	}
	return tx.Rollback()
}

// ServerVersion is the database server's version, when the dialect knows how to ask ("" if not).
func (s *Service) ServerVersion(ctx context.Context) (string, error) {
	v, ok := s.d.(ServerVersioner)
	if !ok {
		return "", nil
	}
	var out string
	if err := s.db.QueryRowContext(ctx, v.ServerVersion()).Scan(&out); err != nil {
		return "", dbError(err)
	}
	return strings.TrimSpace(out), nil
}
