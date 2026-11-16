package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent/fakeportal"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
)

type fixedTunnel struct{ t time.Time }

func (f fixedTunnel) LastHandshake() time.Time { return f.t }

// controller enrolls against the fake portal and builds a Controller with one proxy service.
func controller(t *testing.T, p *fakeportal.Portal, probes ...func(context.Context) error) (*Controller, *logBuffer) {
	t.Helper()
	e, _ := enroller(t, p, p.Token)
	st, err := e.LoadOrEnroll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return controllerFor(t, p, e.Store, st, probes...)
}

func controllerFor(t *testing.T, p *fakeportal.Portal, store state.Store, st *state.State, probes ...func(context.Context) error) (*Controller, *logBuffer) {
	t.Helper()
	cert, err := tls.X509KeyPair([]byte(st.Certificate), []byte(st.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	live := &atomic.Pointer[tls.Certificate]{}
	live.Store(&cert)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(st.CACertificate))
	if len(probes) == 0 {
		probes = []func(context.Context) error{func(context.Context) error { return nil }}
	}
	var services []ServiceRuntime
	for i, probe := range probes {
		name := []string{"certificados", "afiliacion", "hogares"}[i]
		services = append(services, ServiceRuntime{Info: contract.ServiceInfo{Name: name, Path: "/" + name}, Probe: probe})
	}
	logs := &logBuffer{}
	c := NewController(st, store, contract.NewControl(st.ControlURL, p.DialAddress(), roots, live.Load), live,
		services, fixedTunnel{time.Unix(1_800_000_000, 0)}, "0.1.0-dev", slog.New(slog.NewJSONHandler(logs, nil)))
	return c, logs
}

func lastHeartbeat(t *testing.T, p *fakeportal.Portal) contract.HeartbeatRequest {
	t.Helper()
	var hb contract.HeartbeatRequest
	for _, r := range p.Requests() {
		if strings.HasSuffix(r.Path, "/heartbeat") {
			_ = json.Unmarshal(r.Body, &hb)
		}
	}
	return hb
}

func serial(c *tls.Certificate) string {
	x, _ := x509.ParseCertificate(c.Certificate[0])
	return x.SerialNumber.String()
}

func TestHeartbeatCarriesServicesAndHealth(t *testing.T) {
	p := fakeportal.New(t)
	c, _ := controller(t, p)
	wait := c.cycle(context.Background())
	hb := lastHeartbeat(t, p)
	if hb.KitVersion != "0.1.0-dev" || hb.ServicePort != 8443 || len(hb.Services) != 1 || hb.Services[0].Path != "/certificados" || hb.Health.Status != "ok" {
		t.Fatalf("heartbeat: %+v", hb)
	}
	if wait < 54*time.Second || wait > 66*time.Second {
		t.Fatalf("next heartbeat in %v, want 60 s ±10 %%", wait)
	}
	if c.status.PortalState != "online" {
		t.Fatalf("portal state %q", c.status.PortalState)
	}
}

func TestHealthDegradedWhenProbeFails(t *testing.T) {
	p := fakeportal.New(t)
	down := func(context.Context) error { return errors.New("upstream api.interna:443 no responde") }
	up := func(context.Context) error { return nil }
	c, _ := controller(t, p, up, down)
	c.cycle(context.Background())
	if hb := lastHeartbeat(t, p); hb.Health.Status != "degraded" || !strings.Contains(hb.Health.Detail, "afiliacion") {
		t.Fatalf("health: %+v", hb.Health)
	}
	c2, _ := controller(t, fakeportal.New(t), down, down)
	c2.cycle(context.Background())
	if c2.status.Health.Status != "failing" {
		t.Fatalf("all probes failing: %+v", c2.status.Health)
	}
}

func TestControlDownKeepsRetryingWithBackoff(t *testing.T) {
	p := fakeportal.New(t)
	c, _ := controller(t, p)
	p.Set(func(p *fakeportal.Portal) { p.Down = true })
	var waits []time.Duration
	for i := 0; i < 6; i++ {
		waits = append(waits, c.cycle(context.Background()))
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("backoff %v, want %v", waits, want)
		}
	}
	if c.status.PortalState != "unreachable" {
		t.Fatalf("portal state %q", c.status.PortalState)
	}
	// Run never gives up while the portal is down; it returns only when asked to stop.
	c.BackoffMin, c.BackoffMax = time.Millisecond, 5*time.Millisecond
	before := p.Count("/heartbeat")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second) // each attempt is a fresh TLS handshake
	defer cancel()
	if err := c.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run returned %v", err)
	}
	if p.Count("/heartbeat")-before < 3 {
		t.Fatalf("Run stopped retrying: %d attempts", p.Count("/heartbeat")-before)
	}
}

func TestRenewSwapsAndPersists(t *testing.T) {
	p := fakeportal.New(t)
	c, logs := controller(t, p)
	before := serial(c.cert.Load())
	p.Set(func(p *fakeportal.Portal) { p.Heartbeat.RenewCertificate = true })
	c.cycle(context.Background())
	after := serial(c.cert.Load())
	saved, _ := c.store.Load()
	if after == before || saved.PendingRenewal != nil || saved.Certificate == "" || p.Count("/renew") != 1 {
		t.Fatalf("serial %s→%s pending=%v renews=%d", before, after, saved.PendingRenewal, p.Count("/renew"))
	}
	pair, err := tls.X509KeyPair([]byte(saved.Certificate), []byte(saved.KeyPEM))
	if err != nil || serial(&pair) != after {
		t.Fatalf("persisted pair does not match the live certificate: %v", err)
	}
	if !strings.Contains(logs.String(), "certificate_renewed") {
		t.Fatalf("log: %s", logs)
	}
	// The next control call uses the new certificate.
	c.cycle(context.Background())
	if p.Count("/heartbeat") != 2 {
		t.Fatal("heartbeat with the renewed certificate failed")
	}
}

func TestLostRenewResponseRetriesSameCSR(t *testing.T) {
	p := fakeportal.New(t)
	c, _ := controller(t, p)
	p.Set(func(p *fakeportal.Portal) { p.Heartbeat.RenewCertificate = true; p.RenewDrops = 1 })
	c.cycle(context.Background())
	saved, _ := c.store.Load()
	if saved.PendingRenewal == nil {
		t.Fatal("the pending renewal must be persisted before calling /renew")
	}
	p.Set(func(p *fakeportal.Portal) { p.Heartbeat.RenewCertificate = false })
	c.cycle(context.Background())
	csrs := p.RenewCSRs()
	saved, _ = c.store.Load()
	if len(csrs) != 2 || csrs[0] != csrs[1] || saved.PendingRenewal != nil {
		t.Fatalf("renews=%d same=%v pending=%v", len(csrs), len(csrs) == 2 && csrs[0] == csrs[1], saved.PendingRenewal)
	}
}

func TestPendingRenewalResumedAtStart(t *testing.T) {
	p := fakeportal.New(t)
	e, _ := enroller(t, p, p.Token)
	st, _ := e.LoadOrEnroll(context.Background())
	key, csr, _ := NewKeyAndCSR()
	st.PendingRenewal = &state.PendingRenewal{KeyPEM: key, CSRPEM: csr, CreatedAt: time.Now()}
	_ = e.Store.Save(st)
	c, _ := controllerFor(t, p, e.Store, st)
	c.cycle(context.Background())
	if csrs := p.RenewCSRs(); len(csrs) != 1 || csrs[0] != csr {
		t.Fatalf("pending renewal not resumed: %d", len(csrs))
	}
}

func TestRenewRefused409(t *testing.T) {
	p := fakeportal.New(t)
	c, logs := controller(t, p)
	before := serial(c.cert.Load())
	p.Set(func(p *fakeportal.Portal) { p.Heartbeat.RenewCertificate = true; p.RenewStatus = 409 })
	c.cycle(context.Background())
	if serial(c.cert.Load()) != before || !strings.Contains(logs.String(), "renew_refused") {
		t.Fatalf("log: %s", logs)
	}
}

func TestMinimumKitVersionNotice(t *testing.T) {
	p := fakeportal.New(t)
	min := "0.2.0"
	p.Config.MinimumKitVersion = &min
	c, _ := controller(t, p)
	c.cycle(context.Background())
	if !containsNotice(c.status.Notices, "0.2.0") || lastHeartbeat(t, p).Health.Status != "degraded" {
		t.Fatalf("notices=%v health=%+v", c.status.Notices, lastHeartbeat(t, p).Health)
	}
}

func TestUnservedPublishedPathNotice(t *testing.T) {
	p := fakeportal.New(t)
	p.Config.Services = []contract.PublishedService{{APIName: "Afiliación", Version: "1.0", BasePath: "/salud/afiliacion/v1", ServicePath: "/otra"}}
	c, _ := controller(t, p)
	c.cycle(context.Background())
	if !containsNotice(c.status.Notices, "/otra") {
		t.Fatalf("notices=%v", c.status.Notices)
	}
}

func TestRejectedControl(t *testing.T) {
	p := fakeportal.New(t)
	c, _ := controller(t, p)
	p.Set(func(p *fakeportal.Portal) { p.HeartbeatStatus = 401 })
	if wait := c.cycle(context.Background()); wait != 5*time.Minute || c.status.PortalState != "rejected" {
		t.Fatalf("wait=%v state=%q", wait, c.status.PortalState)
	}
}

func TestStatusFileWritten(t *testing.T) {
	p := fakeportal.New(t)
	c, _ := controller(t, p)
	c.cycle(context.Background())
	var s Status
	if err := c.store.ReadStatus(&s); err != nil {
		t.Fatal(err)
	}
	if s.NodeName != p.NodeName || s.PortalState != "online" || len(s.Services) != 1 || s.LastHandshake.IsZero() || s.LastHeartbeat.IsZero() || s.Variant == "" {
		t.Fatalf("status: %+v", s)
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"0.1.0-dev", "0.1.0", -1}, {"0.2.0", "0.1.9", 1}, {"1.0.0", "1.0.0", 0}, {"0.10.0", "0.9.0", 1}, {"1.0.0+build", "1.0.0", 0}}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Fatalf("compareSemver(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func containsNotice(notices []string, s string) bool {
	for _, n := range notices {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// Debt: a 400 means the pending CSR itself is refused: drop it so a fresh key is tried.
func TestRenew400DropsPendingRenewal(t *testing.T) {
	p := fakeportal.New(t)
	c, _ := controller(t, p)
	p.Set(func(p *fakeportal.Portal) { p.Heartbeat.RenewCertificate = true; p.RenewStatus = 400 })
	c.cycle(context.Background())
	saved, _ := c.store.Load()
	if saved.PendingRenewal != nil {
		t.Fatal("a refused CSR must not be retried forever")
	}
	p.Set(func(p *fakeportal.Portal) { p.RenewStatus = 0 })
	c.cycle(context.Background())
	if csrs := p.RenewCSRs(); len(csrs) != 2 || csrs[0] == csrs[1] {
		t.Fatalf("the next renewal must use a fresh CSR: %d", len(csrs))
	}
}

// Debt: a 401 on renew means the grace period is over or the node was decommissioned.
func TestRenew401PointsToReenroll(t *testing.T) {
	p := fakeportal.New(t)
	c, logs := controller(t, p)
	p.Set(func(p *fakeportal.Portal) { p.Heartbeat.RenewCertificate = true; p.RenewStatus = 401 })
	c.cycle(context.Background())
	saved, _ := c.store.Load()
	if saved.PendingRenewal != nil || !strings.Contains(logs.String(), "reenrolar") {
		t.Fatalf("pending=%v log=%s", saved.PendingRenewal, logs)
	}
}

// Debt: pre-release identifiers compare as semver says (numeric ones numerically).
func TestCompareSemverPreRelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"1.0.0-rc.10", "1.0.0-rc.9", 1}, {"1.0.0-alpha", "1.0.0-beta", -1}, {"1.0.0-1", "1.0.0-alpha", -1}, {"1.0.0-alpha", "1.0.0-alpha.1", -1}}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Fatalf("compareSemver(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
