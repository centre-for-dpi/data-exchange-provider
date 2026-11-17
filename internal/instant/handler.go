//go:build !canal

package instant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

const (
	titleTooManyRows = "La clave trae más registros de los que este servicio entrega"
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
	if r.KeyColumn.Kind == KindInteger {
		n, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			server.Problem(w, http.StatusBadRequest, "invalid_key", titleInvalidKey)
			return
		}
		arg = n
	}
	items, err := s.query(req.Context(), r, arg)
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
	for rows.Next() {
		vals := make([]any, len(r.Columns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, dbError(err)
		}
		item := make(map[string]any, len(vals))
		for i, c := range r.Columns {
			spec := r.Resource.Columns[i]
			item[spec.Name] = ToJSON(c, spec.As, vals[i]) // JSON names are the declaration's
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
