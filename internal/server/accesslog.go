package server

import (
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The request id comes from the consumer through the gateway: it could carry an identifier.
// Only a UUID (what the gateway generates) is logged as is.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func requestID(r *http.Request) string {
	id := r.Header.Get("X-Request-Id")
	switch {
	case id == "":
		return ""
	case uuidPattern.MatchString(id):
		return id
	default:
		return "(no uuid)"
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush keeps streaming working through the recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLog writes one "access" event per request: metadata only. Never the real path, the
// query, a body or a header value (CLAUDE.md, rule 2).
func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := "(unknown)"
		assertion := ""
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, withAssertionSlot(withRouteSlot(r, &route), &assertion))
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		client := ""
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			client = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		denHeaders := []string{}
		for name := range r.Header {
			if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-den-") {
				denHeaders = append(denHeaders, lower)
			}
		}
		sort.Strings(denHeaders)
		logger.Info("access",
			"request_id", requestID(r),
			"method", r.Method,
			"route", route,
			"status", rec.status,
			"ms", time.Since(start).Milliseconds(),
			"client", client,
			"den_headers", denHeaders,
			"query_present", r.URL.RawQuery != "",
			"assertion", assertion, // verified | absent | invalid: <reason> | "" (not checked)
		)
	})
}
