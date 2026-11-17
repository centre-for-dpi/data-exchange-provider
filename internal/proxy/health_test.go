package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serve(t *testing.T, s *Service, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	}
}

// K4: an upstream that accepts TCP but does not answer used to probe "ok". Recent 502/503/504
// answers now make the service unhealthy, naming the counts, and it heals once they age out.
func TestProbeReflectsRecentUpstreamErrors(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer hang.Close()
	sp := spec(hang.URL)
	sp.ResponseTimeout = 30 * time.Millisecond
	s, err := NewService("/c", sp)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	s.health.now = func() time.Time { return now }
	if err := s.Probe(context.Background()); err != nil {
		t.Fatalf("no traffic yet: %v", err)
	}
	serve(t, s, 4)
	err = s.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "4 of 4") {
		t.Fatalf("an upstream that times out must be unhealthy: %v", err)
	}
	now = now.Add(61 * time.Second)
	if err := s.Probe(context.Background()); err != nil {
		t.Fatalf("errors older than a minute no longer count: %v", err)
	}
}

func TestProbeIgnoresOccasionalErrors(t *testing.T) {
	var n, down atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if n.Add(1)%5 == 0 {
			w.WriteHeader(http.StatusServiceUnavailable) // the API's own 503 counts as an upstream error
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	s, err := NewService("/c", spec(up.URL))
	if err != nil {
		t.Fatal(err)
	}
	serve(t, s, 10) // 2 errors of 10: below both thresholds
	if err := s.Probe(context.Background()); err != nil {
		t.Fatalf("occasional errors must not flap the health: %v", err)
	}
	down.Store(1)
	serve(t, s, 12) // 12 more errors: 14 of 22
	if err := s.Probe(context.Background()); err == nil {
		t.Fatal("most recent answers are 502: unhealthy")
	}
}
