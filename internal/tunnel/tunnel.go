// Package tunnel runs WireGuard in user space (wireguard-go with its in-process TCP/IP stack):
// no TUN device, no NET_ADMIN, no kernel module. The tunnel address exists only inside this
// process, so the service port cannot be reached from the server's own network.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// Config is the tunnel, as handed out at enrollment.
type Config struct {
	Address      netip.Prefix // the node's /32
	PrivateKey   Key
	HubPublicKey Key
	HubEndpoint  string // host:port; resolved now and every ResolveEvery
	AllowedIPs   []netip.Prefix
	Keepalive    time.Duration
	MTU          int           // default 1420
	ListenPort   int           // 0: random (a node only dials out)
	ResolveEvery time.Duration // default 5m
	// RetryResolveEvery is how often an unresolved hub is retried (default 5s): a DNS outage at
	// startup must not stop the node.
	RetryResolveEvery time.Duration
	Resolve           func(hostport string) (string, error) // default: UDP resolution (tests inject)
}

// Tunnel is a running WireGuard device with its user-space network.
type Tunnel struct {
	cfg    Config
	dev    *device.Device
	net    *netstack.Net
	log    *slog.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	endpoint string // last resolved ip:port
}

// Up starts the device and sets the hub as its only peer.
func Up(ctx context.Context, c Config, log *slog.Logger) (*Tunnel, error) {
	if c.MTU == 0 {
		c.MTU = 1420
	}
	if c.ResolveEvery == 0 {
		c.ResolveEvery = 5 * time.Minute
	}
	if c.RetryResolveEvery == 0 {
		c.RetryResolveEvery = 5 * time.Second
	}
	if c.Resolve == nil {
		c.Resolve = resolve
	}
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{c.Address.Addr()}, nil, c.MTU)
	if err != nil {
		return nil, i18n.Errorf("tunnel: %w", err)
	}
	logger := &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf:   func(format string, args ...any) { log.Warn("wireguard", "detail", fmt.Sprintf(format, args...)) },
	}
	t := &Tunnel{cfg: c, dev: device.NewDevice(tunDev, conn.NewDefaultBind(), logger), net: tnet, log: log}
	var uapi strings.Builder
	fmt.Fprintf(&uapi, "private_key=%s\nlisten_port=%d\npublic_key=%s\n", c.PrivateKey.hex(), c.ListenPort, c.HubPublicKey.hex())
	for _, p := range c.AllowedIPs {
		fmt.Fprintf(&uapi, "allowed_ip=%s\n", p)
	}
	if c.HubEndpoint != "" {
		if ep, err := c.Resolve(c.HubEndpoint); err != nil {
			log.Warn("hub_resolve_failed", "hub", c.HubEndpoint, "error", err.Error(), "detail", "retrying until DNS answers")
		} else {
			t.endpoint = ep
			fmt.Fprintf(&uapi, "endpoint=%s\n", ep)
		}
	}
	if err := t.dev.IpcSet(uapi.String()); err != nil {
		t.dev.Close()
		return nil, i18n.Errorf("tunnel: %w", err)
	}
	if err := t.dev.Up(); err != nil {
		t.dev.Close()
		return nil, i18n.Errorf("tunnel: %w", err)
	}
	if t.endpoint != "" {
		if err := t.startKeepalive(); err != nil {
			t.dev.Close()
			return nil, i18n.Errorf("tunnel: %w", err)
		}
	}
	ctx, t.cancel = context.WithCancel(ctx)
	if c.HubEndpoint != "" {
		t.wg.Add(1)
		go t.reresolve(ctx)
	}
	return t, nil
}

func resolve(hostport string) (string, error) {
	a, err := net.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return "", err
	}
	return a.String(), nil
}

// startKeepalive: set on a running device, the keepalive sends a packet right away, so the hub
// learns where the node is without waiting for traffic (spike S-2).
func (t *Tunnel) startKeepalive() error {
	if t.cfg.Keepalive <= 0 {
		return nil
	}
	return t.dev.IpcSet(fmt.Sprintf("public_key=%s\npersistent_keepalive_interval=%d\n",
		t.cfg.HubPublicKey.hex(), int(t.cfg.Keepalive/time.Second)))
}

// reresolve keeps retrying a hub that did not resolve yet, and then follows it if its name
// moves to another address.
func (t *Tunnel) reresolve(ctx context.Context) {
	defer t.wg.Done()
	for {
		t.mu.Lock()
		resolved := t.endpoint != ""
		t.mu.Unlock()
		wait := t.cfg.ResolveEvery
		if !resolved {
			wait = t.cfg.RetryResolveEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		ep, err := t.cfg.Resolve(t.cfg.HubEndpoint)
		if err != nil {
			t.log.Warn("hub_resolve_failed", "hub", t.cfg.HubEndpoint, "error", err.Error())
			continue
		}
		t.mu.Lock()
		first, changed := t.endpoint == "", ep != t.endpoint
		t.endpoint = ep
		t.mu.Unlock()
		if !changed {
			continue
		}
		if err := t.dev.IpcSet(fmt.Sprintf("public_key=%s\nupdate_only=true\nendpoint=%s\n", t.cfg.HubPublicKey.hex(), ep)); err != nil {
			t.log.Warn("hub_endpoint_update_failed", "error", err.Error())
			continue
		}
		t.log.Info("hub_endpoint_changed", "hub", t.cfg.HubEndpoint, "endpoint", ep)
		if first {
			if err := t.startKeepalive(); err != nil {
				t.log.Warn("keepalive_failed", "error", err.Error())
			}
		}
	}
}

// Listen opens a TCP listener on the tunnel address only.
func (t *Tunnel) Listen(port int) (net.Listener, error) {
	return t.net.ListenTCPAddrPort(netip.AddrPortFrom(t.cfg.Address.Addr(), uint16(port)))
}

// DialTCP opens a TCP connection through the tunnel.
func (t *Tunnel) DialTCP(ctx context.Context, addr netip.AddrPort) (net.Conn, error) {
	return t.net.DialContextTCPAddrPort(ctx, addr)
}

// ListenPort is the UDP port the device listens on (useful when ListenPort was 0).
func (t *Tunnel) ListenPort() int {
	v, _ := t.get("listen_port")
	n, _ := strconv.Atoi(v)
	return n
}

// LastHandshake is the time of the last handshake with the hub (zero if none).
func (t *Tunnel) LastHandshake() time.Time {
	sec, ok := t.get("last_handshake_time_sec")
	if !ok {
		return time.Time{}
	}
	nsec, _ := t.get("last_handshake_time_nsec")
	s, _ := strconv.ParseInt(sec, 10, 64)
	ns, _ := strconv.ParseInt(nsec, 10, 64)
	if s == 0 && ns == 0 {
		return time.Time{}
	}
	return time.Unix(s, ns)
}

func (t *Tunnel) get(key string) (string, bool) {
	out, err := t.dev.IpcGet()
	if err != nil {
		return "", false
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// Close stops the device.
func (t *Tunnel) Close() error {
	t.cancel()
	t.wg.Wait()
	t.dev.Close()
	return nil
}
