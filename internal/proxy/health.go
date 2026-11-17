package proxy

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// Recent upstream errors make a proxy service unhealthy: at least minFailures in the last minute,
// and at least half of the answers. A TCP probe alone says "ok" for an API that accepts the
// connection and never answers.
const (
	healthWindow  = time.Minute
	healthBuckets = 6
	minFailures   = 3
)

type bucket struct {
	slot          int64 // which 10 s slot of time this bucket counts
	total, failed int
}

// health counts the upstream's answers in 10 s buckets over the last minute.
type health struct {
	mu      sync.Mutex
	now     func() time.Time
	buckets [healthBuckets]bucket
}

func newHealth() *health { return &health{now: time.Now} }

func (h *health) slot(t time.Time) int64 {
	return t.UnixNano() / int64(healthWindow/healthBuckets)
}

func (h *health) record(failed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.slot(h.now())
	b := &h.buckets[s%healthBuckets]
	if b.slot != s {
		*b = bucket{slot: s}
	}
	b.total++
	if failed {
		b.failed++
	}
}

func (h *health) recent() (total, failed int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.slot(h.now())
	for _, b := range h.buckets {
		if s-b.slot < healthBuckets {
			total += b.total
			failed += b.failed
		}
	}
	return total, failed
}

// upstreamError is an answer that says the internal API is not working.
func upstreamError(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// Service is a proxy service: its handler, and its health from a TCP probe plus its recent answers.
type Service struct {
	spec    config.ProxySpec
	handler http.Handler
	health  *health
}

// NewService builds a proxy service (see New).
func NewService(servicePath string, spec config.ProxySpec) (*Service, error) {
	h := newHealth()
	handler, err := newHandler(servicePath, spec, h)
	if err != nil {
		return nil, err
	}
	return &Service{spec: spec, handler: handler, health: h}, nil
}

// Handler serves the service.
func (s *Service) Handler() http.Handler { return s.handler }

// Probe fails when the upstream does not accept TCP, or when most of its recent answers were
// errors.
func (s *Service) Probe(ctx context.Context) error {
	if err := Probe(ctx, s.spec); err != nil {
		return err
	}
	if total, failed := s.health.recent(); failed >= minFailures && failed*2 >= total {
		return i18n.Errorf("the internal API answered with an error (502, 503 or 504) in %d of %d requests in the last minute", failed, total)
	}
	return nil
}
