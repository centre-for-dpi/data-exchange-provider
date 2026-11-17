package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
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
	return NewHandlerWithMetrics(nodeName, routes, logger, nil)
}

// NewHandlerWithMetrics is NewHandler that also counts each service's requests in m (nil: no
// counting).
func NewHandlerWithMetrics(nodeName string, routes []Route, logger *slog.Logger, m *Metrics) http.Handler {
	return NewHandlerWith(nodeName, routes, logger, Options{Metrics: m})
}

// NewHandlerWith is NewHandler with its optional parts.
func NewHandlerWith(nodeName string, routes []Route, logger *slog.Logger, o Options) http.Handler {
	m := o.Metrics
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
		if unsafePath(r) {
			SetRoute(r, "(invalid path)")
			Problem(w, http.StatusBadRequest, "invalid_path", "La ruta de la petición no es válida")
			return
		}
		rt := match(rs, r.URL.Path)
		if rt == nil {
			SetRoute(r, "(no service)")
			Problem(w, http.StatusNotFound, "not_found", "Recurso no encontrado")
			return
		}
		SetRoute(r, rt.Path+"/*")
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			m.record(rt.Path, rec.status, time.Since(start))
		}()
		select {
		case rt.slots <- struct{}{}:
			defer func() { <-rt.slots }()
		default:
			Problem(rec, http.StatusServiceUnavailable, "service_busy", "El servicio está ocupado; intenta de nuevo")
			return
		}
		if o.Assertion != nil && !checkAssertion(rec, r, o) {
			return
		}
		rt.Handler.ServeHTTP(rec, strip(r, rt.Path))
	}))
}

// unsafePath rejects what could climb out of a backend's base path once that backend
// normalizes it: empty, "." or ".." segments, backslashes, and encoded dots, slashes or
// backslashes. A trailing slash is allowed.
func unsafePath(r *http.Request) bool {
	escaped := strings.ToLower(r.URL.EscapedPath())
	if strings.Contains(escaped, "%2e") || strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") ||
		strings.Contains(r.URL.Path, "\\") {
		return true
	}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	for i, s := range segments {
		if s == "." || s == ".." || (s == "" && i != len(segments)-1) {
			return true
		}
	}
	return false
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

type assertionKey struct{}

func withAssertionSlot(r *http.Request, slot *string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), assertionKey{}, slot))
}

func setAssertion(r *http.Request, result string) {
	if p, ok := r.Context().Value(assertionKey{}).(*string); ok {
		*p = result
	}
}

// Options are the optional parts of the handler.
type Options struct {
	Metrics *Metrics // per-service counts (nil: none)
	// Assertion checks the gateway's signed assertion of a call (nil: not checked). It receives
	// the X-RID-Assertion header, the method, the URI as received (service path and query) and
	// the body, and returns verified, absent or "invalid: <reason>".
	Assertion func(ctx context.Context, header, method, uri string, body []byte) string
	// RequireAssertion answers 401 to a call whose assertion is not verified.
	RequireAssertion bool
}

// maxAssertionBody is the most of a body the node reads to check its assertion.
const maxAssertionBody = 8 << 20

// checkAssertion reads the body (restoring it for the service) and checks the call. It reports
// whether the call may go on.
func checkAssertion(w http.ResponseWriter, r *http.Request, o Options) bool {
	var body []byte
	if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxAssertionBody+1))
		if err != nil || len(b) > maxAssertionBody {
			setAssertion(r, "invalid: body too large to verify")
			Problem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "El cuerpo de la petición supera el máximo permitido")
			return false
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	result := o.Assertion(r.Context(), r.Header.Get("X-RID-Assertion"), r.Method, r.URL.RequestURI(), body)
	setAssertion(r, result)
	if o.RequireAssertion && result != "verified" {
		Problem(w, http.StatusUnauthorized, "assertion_invalid", "La llamada no trae una aserción válida del gateway")
		return false
	}
	return true
}
