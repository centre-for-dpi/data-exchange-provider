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
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{c.Address.Addr()}, nil, c.MTU)
	if err != nil {
		return nil, fmt.Errorf("túnel: %w", err)
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
		ep, err := resolve(c.HubEndpoint)
		if err != nil {
			t.dev.Close()
			return nil, fmt.Errorf("túnel: no se pudo resolver el hub %s: %w", c.HubEndpoint, err)
		}
		t.endpoint = ep
		fmt.Fprintf(&uapi, "endpoint=%s\n", ep)
	}
	if err := t.dev.IpcSet(uapi.String()); err != nil {
		t.dev.Close()
		return nil, fmt.Errorf("túnel: %w", err)
	}
	if err := t.dev.Up(); err != nil {
		t.dev.Close()
		return nil, fmt.Errorf("túnel: %w", err)
	}
	if c.HubEndpoint != "" && c.Keepalive > 0 {
		// Set on a running device, the keepalive sends a packet right away: the hub learns where
		// the node is without waiting for traffic (spike S-2).
		if err := t.dev.IpcSet(fmt.Sprintf("public_key=%s\npersistent_keepalive_interval=%d\n",
			c.HubPublicKey.hex(), int(c.Keepalive/time.Second))); err != nil {
			t.dev.Close()
			return nil, fmt.Errorf("túnel: %w", err)
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

// reresolve follows the hub if its name moves to another address.
func (t *Tunnel) reresolve(ctx context.Context) {
	defer t.wg.Done()
	tick := time.NewTicker(t.cfg.ResolveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		ep, err := resolve(t.cfg.HubEndpoint)
		if err != nil {
			t.log.Warn("hub_resolve_failed", "hub", t.cfg.HubEndpoint, "error", err.Error())
			continue
		}
		t.mu.Lock()
		changed := ep != t.endpoint
		t.endpoint = ep
		t.mu.Unlock()
		if changed {
			if err := t.dev.IpcSet(fmt.Sprintf("public_key=%s\nupdate_only=true\nendpoint=%s\n", t.cfg.HubPublicKey.hex(), ep)); err != nil {
				t.log.Warn("hub_endpoint_update_failed", "error", err.Error())
			} else {
				t.log.Info("hub_endpoint_changed", "hub", t.cfg.HubEndpoint, "endpoint", ep)
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
