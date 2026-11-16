package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
)

// ServiceRuntime is a served service: what the heartbeat declares and how to check it.
type ServiceRuntime struct {
	Info  contract.ServiceInfo
	Probe func(context.Context) error
}

// Controller runs the control cycle: heartbeat with health, renewal, /config and status.json.
// The data plane does not depend on it: if the portal is down, the node keeps serving.
type Controller struct {
	st       *state.State
	store    state.Store
	control  *contract.Control
	cert     *atomic.Pointer[tls.Certificate]
	services []ServiceRuntime
	tunnel   interface{ LastHandshake() time.Time }
	kit      string
	log      *slog.Logger

	Now           func() time.Time
	BackoffMin    time.Duration // 5s
	BackoffMax    time.Duration // 60s
	ConfigEvery   time.Duration // 10m
	RejectedEvery time.Duration // 5m
	ProbeTimeout  time.Duration // 3s
	Interval      time.Duration // non-zero: fixed heartbeat interval (development)

	failures   int
	lastConfig time.Time
	notices    []string
	status     Status
}

// NewController builds the controller with its defaults.
func NewController(st *state.State, store state.Store, control *contract.Control, cert *atomic.Pointer[tls.Certificate],
	services []ServiceRuntime, tun interface{ LastHandshake() time.Time }, kitVersion string, log *slog.Logger) *Controller {
	return &Controller{
		st: st, store: store, control: control, cert: cert, services: services, tunnel: tun, kit: kitVersion, log: log,
		Now: time.Now, BackoffMin: 5 * time.Second, BackoffMax: 60 * time.Second,
		ConfigEvery: 10 * time.Minute, RejectedEvery: 5 * time.Minute, ProbeTimeout: 3 * time.Second,
		status: Status{NodeID: st.NodeID, NodeName: st.NodeName, PortalState: "starting", KitVersion: kitVersion, Variant: buildinfo.Variant()},
	}
}

// Run repeats the control cycle until ctx ends.
func (c *Controller) Run(ctx context.Context) error {
	for {
		wait := c.cycle(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// cycle does one round and returns how long to wait before the next.
func (c *Controller) cycle(ctx context.Context) time.Duration {
	if c.st.PendingRenewal != nil {
		c.renew(ctx) // resume a renewal whose answer was lost, even if the portal no longer asks
	}
	services, health := c.probe(ctx)
	if c.lastConfig.IsZero() || c.Now().Sub(c.lastConfig) >= c.ConfigEvery {
		c.refreshConfig(ctx)
	}
	if len(c.notices) > 0 && health.Status == "ok" {
		health = contract.Health{Status: "degraded", Detail: truncate(c.notices[0], 200)}
	}
	infos := make([]contract.ServiceInfo, 0, len(c.services))
	for _, s := range c.services {
		infos = append(infos, s.Info)
	}
	res, err := c.control.Heartbeat(ctx, c.st.NodeID, contract.HeartbeatRequest{
		KitVersion: c.kit, ServicePort: 8443, Services: infos, Health: health,
	})
	var wait time.Duration
	switch status := contract.StatusOf(err); {
	case err == nil:
		c.failures = 0
		c.status.PortalState = res.Status
		c.status.LastHeartbeat = c.Now()
		wait = c.nextInterval(res.NextHeartbeatSeconds)
		if res.RenewCertificate {
			c.renew(ctx)
		}
	case status == http.StatusUnauthorized:
		c.status.PortalState = "rejected"
		c.log.Error("control_rejected", "detail", "el portal rechazó el certificado del nodo: el nodo fue dado de baja o su certificado venció; usa install.sh reenrolar")
		wait = c.RejectedEvery
	default:
		c.status.PortalState = "unreachable"
		wait = min(c.BackoffMin<<min(c.failures, 16), c.BackoffMax)
		c.failures++
		c.log.Warn("heartbeat_failed", "error", err.Error(), "retry_in", wait.String())
	}
	c.status.Health, c.status.Services, c.status.Notices = health, services, c.notices
	c.status.LastHandshake = c.tunnel.LastHandshake()
	c.status.CertificateNotAfter = c.st.CertificateNotAfter
	c.status.UpdatedAt = c.Now()
	if err := c.store.WriteStatus(c.status); err != nil {
		c.log.Warn("status_write_failed", "error", err.Error())
	}
	return wait
}

func (c *Controller) nextInterval(next int) time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	if next <= 0 {
		next = c.st.HeartbeatIntervalSeconds
	}
	if next <= 0 {
		next = 60
	}
	base := time.Duration(next) * time.Second
	return base + time.Duration((rand.Float64()*0.2-0.1)*float64(base)) // ±10 % jitter
}

// probe checks every service concurrently: ok, degraded (some fail) or failing (all fail).
func (c *Controller) probe(ctx context.Context) ([]ServiceStatus, contract.Health) {
	out := make([]ServiceStatus, len(c.services))
	var wg sync.WaitGroup
	for i, s := range c.services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, c.ProbeTimeout)
			defer cancel()
			out[i] = ServiceStatus{Name: s.Info.Name, Path: s.Info.Path, Healthy: true}
			if s.Probe != nil {
				if err := s.Probe(pctx); err != nil {
					out[i].Healthy, out[i].Detail = false, err.Error()
				}
			}
		}()
	}
	wg.Wait()
	var failing []string
	for _, s := range out {
		if !s.Healthy {
			failing = append(failing, s.Name)
		}
	}
	switch {
	case len(failing) == 0:
		return out, contract.Health{Status: "ok"}
	case len(failing) == len(out):
		return out, contract.Health{Status: "failing", Detail: truncate("sin respuesta: "+strings.Join(failing, ", "), 200)}
	default:
		return out, contract.Health{Status: "degraded", Detail: truncate("sin respuesta: "+strings.Join(failing, ", "), 200)}
	}
}

// refreshConfig reads what the network expects and turns mismatches into notices.
func (c *Controller) refreshConfig(ctx context.Context) {
	cfg, err := c.control.Config(ctx, c.st.NodeID)
	if err != nil {
		c.log.Warn("config_failed", "error", err.Error())
		return
	}
	c.lastConfig = c.Now()
	var notices []string
	if m := cfg.MinimumKitVersion; m != nil && *m != "" && compareSemver(c.kit, *m) < 0 {
		notices = append(notices, fmt.Sprintf("la versión del kit %s es menor que la mínima exigida %s: actualiza con install.sh update", c.kit, *m))
	}
	local := map[string]bool{}
	for _, s := range c.services {
		local[s.Info.Path] = true
	}
	for _, s := range cfg.Services {
		if !local[s.ServicePath] {
			notices = append(notices, fmt.Sprintf("la versión %s de %s está publicada en %s pero este nodo no sirve la ruta %s", s.Version, s.APIName, s.BasePath, s.ServicePath))
		}
	}
	for _, n := range cfg.Notices {
		notices = append(notices, "portal: "+n.Message)
	}
	for _, n := range notices {
		c.log.Warn("notice", "detail", n)
	}
	c.notices = notices
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
