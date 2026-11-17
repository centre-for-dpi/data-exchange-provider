package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/assertion"
	"github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
	"github.com/centre-for-dpi/data-exchange-provider/internal/proxy"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
)

const servicePort = 8443

type runOptions struct {
	ConfigPath, StateDir, TokenFile string
	PortalRoots                     *x509.CertPool // nil: the system's roots
	HeartbeatEvery                  time.Duration  // non-zero: fixed interval (development)
	Log                             *slog.Logger
}

// runNode is the node: services first (a broken configuration never enrolls), then the
// identity, the tunnel, the server on the tunnel and the control cycle. It returns when ctx ends.
func runNode(ctx context.Context, o runOptions) error {
	log := o.Log
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return err
	}
	var routes []server.Route
	var services []agent.ServiceRuntime
	for _, s := range cfg.Services {
		if s.Type == "instant" {
			if len(s.Instant.Resources) == 0 {
				log.Warn("instant service without resources: not served", "service", s.Name)
				continue
			}
			route, rt, closeFn, err := instantRuntime(ctx, s, log)
			if err != nil {
				return i18n.Errorf("service %q: %w", s.Name, err)
			}
			defer closeFn() // after the server has drained (runNode returns after Serve)
			routes = append(routes, route)
			services = append(services, rt)
			continue
		}
		ps, err := proxy.NewService(s.Path, s.Proxy)
		if err != nil {
			return i18n.Errorf("service %q: %w", s.Name, err)
		}
		routes = append(routes, server.Route{Path: s.Path, Handler: ps.Handler(), MaxRequestBytes: s.Proxy.MaxRequestBytes})
		info := contract.ServiceInfo{Name: s.Name, Path: s.Path}
		if s.Proxy.OpenAPI != "" {
			if b, err := os.ReadFile(s.Proxy.OpenAPI); err == nil {
				sum := sha256.Sum256(b)
				info.OpenAPISHA256 = hex.EncodeToString(sum[:])
			}
		}
		services = append(services, agent.ServiceRuntime{Info: info, Probe: ps.Probe})
	}
	// Each service's counts and latency go to status.json (the node opens no metrics port).
	metrics := server.NewMetrics()
	for i := range services {
		path := services[i].Info.Path
		services[i].Stats = func() agent.ServiceStats {
			s := metrics.Snapshot(path)
			return agent.ServiceStats{Requests: s.Requests, ClientErrors: s.ClientErrors, ServerErrors: s.ServerErrors, P50: s.P50, P95: s.P95}
		}
	}
	log.Info("starting", "version", buildinfo.Version, "variant", buildinfo.Variant(), "services", len(routes))

	enroller := &agent.Enroller{Store: state.Store{Dir: o.StateDir}, HTTP: contract.NewPortalHTTPClient(o.PortalRoots),
		Portal: cfg.Portal, TokenFile: o.TokenFile, KitVersion: buildinfo.Version, ServicePort: servicePort, Log: log}
	st, err := enroller.LoadOrEnroll(ctx)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair([]byte(st.Certificate), []byte(st.KeyPEM))
	if err != nil {
		return i18n.Errorf("the node's certificate does not match its key: %w; use install.sh reenroll", err)
	}
	live := &atomic.Pointer[tls.Certificate]{}
	live.Store(&cert)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(st.CACertificate)) {
		return i18n.New("the state does not have the network's root; use install.sh reenroll")
	}

	opts, err := assertionOptions(cfg, st, o.PortalRoots, log)
	if err != nil {
		return err
	}
	opts.Metrics = metrics

	tcfg, err := tunnelConfig(st)
	if err != nil {
		return err
	}
	tun, err := tunnel.Up(ctx, tcfg, log)
	if err != nil {
		return err
	}
	defer tun.Close()
	l, err := tun.Listen(servicePort)
	if err != nil {
		return err
	}
	log.Info("serving", "node", st.NodeName, "address", tcfg.Address.Addr().String(), "port", servicePort, "only_client", st.GatewayClientName)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(ctx, l, server.TLSConfig(server.Identity{NodeName: st.NodeName, GatewayClientName: st.GatewayClientName,
			Roots: roots, Certificate: live.Load}), server.NewHandlerWith(st.NodeName, routes, log, opts), log)
	}()
	ctl := agent.NewController(st, enroller.Store, contract.NewControl(st.ControlURL, cfg.ControlDialAddress, roots, live.Load),
		live, services, tun, buildinfo.Version, log)
	ctl.Interval = o.HeartbeatEvery
	go func() { _ = ctl.Run(ctx) }()

	select {
	case <-ctx.Done():
		<-serveErr
		log.Info("stopped")
		return nil
	case err := <-serveErr:
		return err
	}
}

func tunnelConfig(st *state.State) (tunnel.Config, error) {
	w := st.WireGuard
	addr, err := netip.ParsePrefix(w.Address)
	if err != nil {
		return tunnel.Config{}, i18n.Errorf("invalid tunnel address %q: %w", w.Address, err)
	}
	priv, err := tunnel.ParseKey(st.WGPrivateKey)
	if err != nil {
		return tunnel.Config{}, err
	}
	hub, err := tunnel.ParseKey(w.HubPublicKey)
	if err != nil {
		return tunnel.Config{}, err
	}
	var allowed []netip.Prefix
	for _, a := range w.AllowedIPs {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return tunnel.Config{}, i18n.Errorf("invalid allowedIps %q: %w", a, err)
		}
		allowed = append(allowed, p)
	}
	keepalive := time.Duration(w.PersistentKeepaliveSeconds) * time.Second
	if keepalive == 0 {
		keepalive = 25 * time.Second
	}
	return tunnel.Config{Address: addr, PrivateKey: priv, HubPublicKey: hub, HubEndpoint: w.HubEndpoint,
		AllowedIPs: allowed, Keepalive: keepalive}, nil
}

// assertionAudience is the provider entity's slug the gateway's assertions are addressed to: the
// configured one, or the one in the gateway's client name (<prefix>.<slug>). Unknown, the
// verification is off (a warning), or the node does not start when it is required.
func assertionAudience(a config.GatewayAssertion, gatewayClientName string) (string, error) {
	if a.Audience != "" {
		return a.Audience, nil
	}
	// The portal names the gateway's client <prefix>.<slug>; the prefix is the portal's setting.
	if _, slug, ok := strings.Cut(gatewayClientName, "."); ok && slug != "" {
		return slug, nil
	}
	if a.Mode == "require" {
		return "", i18n.Errorf("gatewayAssertion: cannot tell the entity's slug from %q; set it in gatewayAssertion.audience", gatewayClientName)
	}
	return "", nil
}

// assertionOptions builds the server's assertion check from the configuration.
func assertionOptions(cfg *config.Node, st *state.State, portalRoots *x509.CertPool, log *slog.Logger) (server.Options, error) {
	a := cfg.GatewayAssertion
	if a.Mode == "off" {
		log.Info("gateway assertion", "mode", "off")
		return server.Options{}, nil
	}
	audience, err := assertionAudience(a, st.GatewayClientName)
	if err != nil {
		return server.Options{}, err
	}
	if audience == "" {
		log.Warn("gateway assertion not verified: unknown provider slug; set gatewayAssertion.audience", "gateway_client", st.GatewayClientName)
		return server.Options{}, nil
	}
	v := assertion.New(assertion.Options{
		JWKSURL: strings.TrimRight(cfg.Portal, "/") + "/api/v1/gateway/jwks", Audience: audience, Issuer: a.Issuer,
		HTTP: contract.NewPortalHTTPClient(portalRoots), Log: log,
	})
	log.Info("gateway assertion", "mode", a.Mode, "audience", audience)
	return server.Options{
		Assertion: func(ctx context.Context, header, method, uri string, body []byte) string {
			return string(v.Check(ctx, header, method, uri, body))
		},
		RequireAssertion: a.Mode == "require",
	}, nil
}
