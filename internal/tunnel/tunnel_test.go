package tunnel_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel/tunneltest"
)

const testPort = 18443

var nodeAddr = netip.MustParsePrefix("10.77.0.2/32")

// upNode brings up a node tunnel to a fresh test hub and returns both ends.
func upNode(t *testing.T) (*tunnel.Tunnel, func(context.Context, netip.AddrPort) (net.Conn, error)) {
	t.Helper()
	priv, pub := tunnel.GenerateKey()
	hubPub, endpoint, dial := tunneltest.Hub(t, pub, nodeAddr)
	node, err := tunnel.Up(context.Background(), tunnel.Config{
		Address: nodeAddr, PrivateKey: priv, HubPublicKey: hubPub, HubEndpoint: endpoint,
		AllowedIPs: []netip.Prefix{netip.PrefixFrom(tunneltest.HubAddress, 32)}, Keepalive: 25 * time.Second,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	return node, dial
}

func TestTunnelEcho(t *testing.T) {
	node, dial := upNode(t)
	l, err := node.Listen(testPort)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	// The hub has never seen the node: the node's first packet must teach it where the node is.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dial(ctx, netip.AddrPortFrom(nodeAddr.Addr(), testPort))
	if err != nil {
		t.Fatalf("hub could not reach the node within 3 s: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo: %q %v", buf, err)
	}
}

func TestLastHandshakeSet(t *testing.T) {
	node, dial := upNode(t)
	l, _ := node.Listen(testPort)
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, err := dial(ctx, netip.AddrPortFrom(nodeAddr.Addr(), testPort)); err == nil {
		c.Close()
	} else {
		t.Fatal(err)
	}
	if hs := node.LastHandshake(); hs.IsZero() || time.Since(hs) > time.Minute {
		t.Fatalf("LastHandshake = %v", hs)
	}
}

func TestKeyRoundTrip(t *testing.T) {
	priv, pub := tunnel.GenerateKey()
	if tunnel.PublicKey(priv) != pub {
		t.Fatal("PublicKey(priv) != pub")
	}
	parsed, err := tunnel.ParseKey(pub.String())
	if err != nil || parsed != pub {
		t.Fatalf("ParseKey(String()) = %v, %v", parsed, err)
	}
	if _, err := tunnel.ParseKey("not-a-key"); err == nil {
		t.Fatal("ParseKey must reject garbage")
	}
}

func TestServicePortNotOnHost(t *testing.T) {
	node, _ := upNode(t)
	if _, err := node.Listen(testPort); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:18443", time.Second); err == nil {
		c.Close()
		t.Fatal("the service port must exist only inside the tunnel")
	}
}

// Debt: a DNS outage at startup must not be fatal; the tunnel comes up when the hub resolves.
func TestHubResolvedLaterNotFatal(t *testing.T) {
	priv, pub := tunnel.GenerateKey()
	hubPub, endpoint, dial := tunneltest.Hub(t, pub, nodeAddr)
	var calls atomic.Int32
	node, err := tunnel.Up(context.Background(), tunnel.Config{
		Address: nodeAddr, PrivateKey: priv, HubPublicKey: hubPub, HubEndpoint: "wg-hub.invalid:51820",
		AllowedIPs: []netip.Prefix{netip.PrefixFrom(tunneltest.HubAddress, 32)}, Keepalive: 25 * time.Second,
		RetryResolveEvery: 20 * time.Millisecond,
		Resolve: func(string) (string, error) {
			if calls.Add(1) < 3 {
				return "", errors.New("no such host")
			}
			return endpoint, nil
		},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("a DNS failure at startup must not be fatal: %v", err)
	}
	t.Cleanup(func() { _ = node.Close() })
	l, _ := node.Listen(testPort)
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		c, err := dial(ctx, netip.AddrPortFrom(nodeAddr.Addr(), testPort))
		if err == nil {
			c.Close()
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("the tunnel never came up after the hub resolved: %v", err)
		}
	}
}
