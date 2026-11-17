package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// K4: per-service counts and latency, for status (the node opens no port for a metrics endpoint).
func TestMetricsPerService(t *testing.T) {
	m := NewMetrics()
	answer := func(code int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
	}
	h := NewHandlerWithMetrics(nodeName, []Route{
		{Path: "/salud", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/caida" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			time.Sleep(2 * time.Millisecond)
			_, _ = w.Write([]byte("ok"))
		})},
		{Path: "/certificados", Handler: answer(http.StatusNotFound)},
	}, slog.New(slog.DiscardHandler), m)
	get := func(path string) {
		req := httptest.NewRequest(http.MethodGet, "https://"+nodeName+path, nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	for i := 0; i < 5; i++ {
		get("/salud/afiliados/1")
	}
	get("/salud/caida")
	get("/certificados/x")
	get("/otro") // no service: not counted
	salud, cert := m.Snapshot("/salud"), m.Snapshot("/certificados")
	if salud.Requests != 6 || salud.OK != 5 || salud.ServerErrors != 1 || salud.ClientErrors != 0 || salud.P50 < time.Millisecond || salud.P95 < salud.P50 {
		t.Fatalf("salud: %+v", salud)
	}
	if cert.Requests != 1 || cert.ClientErrors != 1 {
		t.Fatalf("certificados: %+v", cert)
	}
	if none := m.Snapshot("/otro"); none.Requests != 0 {
		t.Fatalf("unrouted requests are not a service's: %+v", none)
	}
}
