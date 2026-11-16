// Package tunneltest is a WireGuard hub for tests: the network's hub, in user space, on
// 127.0.0.1 with a random port. Production code never imports it.
package tunneltest

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
)

// HubAddress is the hub's tunnel address (the first of the network's subnet).
var HubAddress = netip.MustParseAddr("10.77.0.1")

// Hub starts a hub that accepts one node and returns its public key, its endpoint and a dialer
// that opens TCP connections to the node through the tunnel (as the hub's nginx stream does).
func Hub(t testing.TB, nodePublic tunnel.Key, nodeAddr netip.Prefix) (hubPublic tunnel.Key, endpoint string,
	dial func(ctx context.Context, addr netip.AddrPort) (net.Conn, error)) {
	t.Helper()
	priv, pub := tunnel.GenerateKey()
	hub, err := tunnel.Up(context.Background(), tunnel.Config{
		Address: netip.PrefixFrom(HubAddress, 32), PrivateKey: priv, HubPublicKey: nodePublic,
		AllowedIPs: []netip.Prefix{nodeAddr},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	return pub, fmt.Sprintf("127.0.0.1:%d", hub.ListenPort()), hub.DialTCP
}
