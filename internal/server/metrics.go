package server

import (
	"sort"
	"sync"
	"time"
)

// latencySamples is how many recent latencies each service keeps for its percentiles.
const latencySamples = 1024

// Metrics counts each service's requests since the node started, by class of answer, and keeps
// its recent latencies. Metadata only, like the access log.
type Metrics struct {
	mu       sync.Mutex
	services map[string]*serviceMetrics
}

type serviceMetrics struct {
	requests, ok, clientErrors, serverErrors int64
	latencies                                [latencySamples]time.Duration
	n                                        int // latencies written (ring index = n % latencySamples)
}

// ServiceSnapshot is one service's numbers.
type ServiceSnapshot struct {
	Requests     int64         // since the node started
	OK           int64         // 1xx–3xx
	ClientErrors int64         // 4xx
	ServerErrors int64         // 5xx
	P50, P95     time.Duration // over the last latencySamples requests
}

// NewMetrics is empty metrics.
func NewMetrics() *Metrics { return &Metrics{services: map[string]*serviceMetrics{}} }

func (m *Metrics) record(path string, status int, took time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.services[path]
	if s == nil {
		s = &serviceMetrics{}
		m.services[path] = s
	}
	s.requests++
	switch {
	case status >= 500:
		s.serverErrors++
	case status >= 400:
		s.clientErrors++
	default:
		s.ok++
	}
	s.latencies[s.n%latencySamples] = took
	s.n++
}

// Snapshot is the numbers of the service at path (zero if it has had no requests).
func (m *Metrics) Snapshot(path string) ServiceSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.services[path]
	if s == nil {
		return ServiceSnapshot{}
	}
	n := min(s.n, latencySamples)
	recent := make([]time.Duration, n)
	copy(recent, s.latencies[:n])
	sort.Slice(recent, func(i, j int) bool { return recent[i] < recent[j] })
	snap := ServiceSnapshot{Requests: s.requests, OK: s.ok, ClientErrors: s.clientErrors, ServerErrors: s.serverErrors}
	if n > 0 {
		snap.P50, snap.P95 = recent[(n-1)*50/100], recent[(n-1)*95/100]
	}
	return snap
}
