//go:build !canal

package instant

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

const (
	titleTooManyRows = "La clave trae más registros de los que este servicio entrega"
	titleTooLarge    = "La respuesta es más grande de lo que este servicio entrega"
	titleNotFitting  = "Un valor de la fuente no se puede representar con el tipo que declara el OpenAPI"
	titleMethod      = "Este servicio solo acepta GET"
	// maxKeyBytes bounds any key, whatever the declared pattern.
	maxKeyBytes = 256
)

func (s *Service) serve(w http.ResponseWriter, req *http.Request) {
	base := s.cfg.Path
	if req.Method != http.MethodGet {
		server.SetRoute(req, base+"/*")
		w.Header().Set("Allow", http.MethodGet)
		server.Problem(w, http.StatusMethodNotAllowed, "method_not_allowed", titleMethod)
		return
	}
	path := req.URL.EscapedPath()
	if path == "/" || path == "" {
		server.SetRoute(req, base+"/")
		s.root(w, req)
		return
	}
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	set := s.set.Load()
	var r *Compiled
	if len(segs) == 2 && segs[1] != "" && set != nil {
		r = set.byName[segs[0]]
	}
	if r == nil {
		server.SetRoute(req, base+"/(undeclared)")
		if set == nil {
			server.Problem(w, http.StatusServiceUnavailable, "service_unavailable", titleUnavailable)
			return
		}
		server.Problem(w, http.StatusNotFound, "not_found", "Recurso no encontrado")
		return
	}
	server.SetRoute(req, base+"/"+r.Name+"/{"+r.Key.Column+"}")
	if req.URL.RawQuery != "" || req.URL.ForceQuery {
		server.Problem(w, http.StatusBadRequest, "query_not_allowed", titleQueryNotAllowed)
		return
	}
	key, err := url.PathUnescape(segs[1])
	if err != nil || len(key) > maxKeyBytes || !r.KeyRegexp.MatchString(key) {
		server.Problem(w, http.StatusBadRequest, "invalid_key", titleInvalidKey)
		return
	}
	var arg any = key
	switch r.KeyColumn.Kind {
	case KindInteger:
		n, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			// Beyond int64: a decimal column (Oracle NUMBER(p,0), up to 38 digits) takes it as
			// text; a binary integer column cannot hold it, so no row has it.
			if decimalNative(r.KeyColumn.Native) {
				arg = key
				break
			}
			server.Problem(w, http.StatusNotFound, "not_found", titleNotFound)
			return
		}
		if strings.EqualFold(r.KeyColumn.Native, "integer") && (n > math.MaxInt32 || n < math.MinInt32) {
			// PostgreSQL's 4-byte integer: no row can have it (and the driver refuses to bind it).
			server.Problem(w, http.StatusNotFound, "not_found", titleNotFound)
			return
		}
		arg = n
	case KindDate:
		if _, err := time.Parse("2006-01-02", key); err != nil {
			server.Problem(w, http.StatusBadRequest, "invalid_key", titleInvalidKey)
			return
		}
	}
	if b, ok := s.d.(KeyBinder); ok && r.KeyColumn.Kind != KindInteger {
		arg = b.BindKey(r.KeyColumn, key)
	}
	items, err := s.query(req.Context(), r, arg)
	if errors.Is(err, errTooLarge) {
		server.Problem(w, http.StatusInternalServerError, "response_too_large", titleTooLarge)
		return
	}
	var nf *notFitting
	if errors.As(err, &nf) {
		s.log.Warn("unrepresentable value", "resource", r.Name, "column", nf.column, "kind", string(nf.kind))
		server.Problem(w, http.StatusInternalServerError, "unrepresentable_value", titleNotFitting)
		return
	}
	s.served(err)
	if err != nil {
		s.logError("database error", err, "resource", r.Name)
		server.Problem(w, http.StatusServiceUnavailable, "service_unavailable", titleUnavailable)
		return
	}
	switch {
	case len(items) == 0:
		server.Problem(w, http.StatusNotFound, "not_found", titleNotFound)
	case r.Many && len(items) > r.MaxRows:
		server.Problem(w, http.StatusInternalServerError, "too_many_rows", titleTooManyRows)
	case r.Many:
		writeJSON(w, items)
	case len(items) > 1:
		server.Problem(w, http.StatusInternalServerError, "non_unique_key", titleNonUnique)
	default:
		writeJSON(w, items[0])
	}
}

// query runs the resource's SELECT in a read-only transaction, bounded by queryTimeout.
func (s *Service) query(ctx context.Context, r *Compiled, arg any) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Instant.Database.QueryTimeout)
	defer cancel()
	tx, err := s.d.BeginReadOnly(ctx, s.db)
	if err != nil {
		return nil, dbError(err)
	}
	defer func() { _ = tx.Rollback() }() // read-only: nothing to commit
	rows, err := tx.QueryContext(ctx, r.Query, arg)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	var items []map[string]any
	size := 0
	for rows.Next() {
		vals := make([]any, len(r.Columns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, dbError(err)
		}
		for _, v := range vals {
			size += valueSize(v)
		}
		if size > s.maxResponseBytes {
			return nil, errTooLarge
		}
		item := make(map[string]any, len(vals))
		for i, c := range r.Columns {
			spec := r.Resource.Columns[i]
			v := ToJSON(c, spec.As, vals[i]) // JSON names are the declaration's
			if !fits(r.Exposed[i], v) {
				return nil, &notFitting{column: spec.Name, kind: r.Exposed[i]}
			}
			item[spec.Name] = v
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, dbError(err)
	}
	return items, nil
}

// root answers the portal's healthcheck: 200 while the database answers, 503 otherwise.
func (s *Service) root(w http.ResponseWriter, req *http.Request) {
	if err := s.Probe(req.Context()); err != nil {
		server.Problem(w, http.StatusServiceUnavailable, "service_unavailable", titleUnavailable)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// decimalNative is a column type that stores integers as decimals of any length.
func decimalNative(native string) bool {
	switch strings.ToLower(strings.TrimSpace(native)) {
	case "number", "numeric", "decimal":
		return true
	}
	return false
}

// notFitting is a value that ToJSON could not turn into its column's exposed kind (NaN, Infinity,
// a zero date): serving it would break the OpenAPI.
type notFitting struct {
	column string
	kind   Kind
}

func (e *notFitting) Error() string { return "value does not fit " + string(e.kind) }

// errTooLarge stops reading rows once a response passes maxResponseBytes.
var errTooLarge = errors.New("response too large")

// valueSize approximates what a scanned value adds to the response.
func valueSize(v any) int {
	switch x := v.(type) {
	case string:
		return len(x) + 8
	case []byte:
		return len(x) + 8
	}
	return 32
}

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil { // ToJSON only yields marshalable values; this is a bug, not data
		server.Problem(w, http.StatusInternalServerError, "internal_error", "Error interno del nodo")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(b, '\n'))
}
