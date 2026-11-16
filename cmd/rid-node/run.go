package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"sync/atomic"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
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
		h, err := proxy.New(s.Path, s.Proxy)
		if err != nil {
			return fmt.Errorf("servicio %q: %w", s.Name, err)
		}
		routes = append(routes, server.Route{Path: s.Path, Handler: h})
		info := contract.ServiceInfo{Name: s.Name, Path: s.Path}
		if s.Proxy.OpenAPI != "" {
			if b, err := os.ReadFile(s.Proxy.OpenAPI); err == nil {
				sum := sha256.Sum256(b)
				info.OpenAPISHA256 = hex.EncodeToString(sum[:])
			}
		}
		spec := s.Proxy
		services = append(services, agent.ServiceRuntime{Info: info, Probe: func(ctx context.Context) error { return proxy.Probe(ctx, spec) }})
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
		return fmt.Errorf("el certificado del nodo no corresponde a su clave: %w; usa install.sh reenrolar", err)
	}
	live := &atomic.Pointer[tls.Certificate]{}
	live.Store(&cert)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(st.CACertificate)) {
		return errors.New("el estado no tiene la raíz de la red; usa install.sh reenrolar")
	}

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
			Roots: roots, Certificate: live.Load}), server.NewHandler(st.NodeName, routes, log), log)
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
		return tunnel.Config{}, fmt.Errorf("dirección del túnel %q inválida: %w", w.Address, err)
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
			return tunnel.Config{}, fmt.Errorf("allowedIps %q inválida: %w", a, err)
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
