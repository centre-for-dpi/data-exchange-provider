package server

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// Route is one service behind its servicePath.
type Route struct {
	Path        string
	Handler     http.Handler
	MaxInFlight int // concurrent requests to protect the entity's backend; default 32
}

type route struct {
	Route
	slots chan struct{}
}

type routeKey struct{}

// SetRoute records the route template for the access log. The real path is never logged.
func SetRoute(r *http.Request, template string) {
	if p, ok := r.Context().Value(routeKey{}).(*string); ok {
		*p = template
	}
}

// NewHandler checks Host, picks the route by longest servicePath prefix, strips it, limits
// concurrency per route and writes one access line per request.
func NewHandler(nodeName string, routes []Route, logger *slog.Logger) http.Handler {
	rs := make([]*route, 0, len(routes))
	for _, r := range routes {
		if r.MaxInFlight <= 0 {
			r.MaxInFlight = 32
		}
		rs = append(rs, &route{Route: r, slots: make(chan struct{}, r.MaxInFlight)})
	}
	sort.Slice(rs, func(i, j int) bool { return len(rs[i].Path) > len(rs[j].Path) })

	return accessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := strings.Cut(r.Host, ":"); host != nodeName {
			SetRoute(r, "(wrong host)")
			Problem(w, http.StatusMisdirectedRequest, "misdirected_request", "La petición no es para este nodo")
			return
		}
		rt := match(rs, r.URL.Path)
		if rt == nil {
			SetRoute(r, "(no service)")
			Problem(w, http.StatusNotFound, "not_found", "Recurso no encontrado")
			return
		}
		SetRoute(r, rt.Path+"/*")
		select {
		case rt.slots <- struct{}{}:
			defer func() { <-rt.slots }()
		default:
			Problem(w, http.StatusServiceUnavailable, "service_busy", "El servicio está ocupado; intenta de nuevo")
			return
		}
		rt.Handler.ServeHTTP(w, strip(r, rt.Path))
	}))
}

func match(rs []*route, path string) *route {
	for _, r := range rs {
		if path == r.Path || strings.HasPrefix(path, r.Path+"/") {
			return r
		}
	}
	return nil
}

// strip removes the servicePath prefix (the rest is what the backend sees; "/" at least).
func strip(r *http.Request, prefix string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
	if r2.URL.Path == "" {
		r2.URL.Path = "/"
	}
	if r.URL.RawPath != "" {
		r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
		if r2.URL.RawPath == "" {
			r2.URL.RawPath = "/"
		}
	}
	return r2
}

func withRouteSlot(r *http.Request, slot *string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), routeKey{}, slot))
}
