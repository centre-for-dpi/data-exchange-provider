// THROWAWAY SPIKE. WireGuard in user space: wireguard-go with its in-process TCP/IP stack
// (tun/netstack, gVisor). No TUN device, no wintun, no NET_ADMIN, no admin rights: the tunnel
// address exists only inside this process.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type wgKey struct{ private, public [32]byte }

func newWGKey() wgKey {
	var k wgKey
	_, _ = rand.Read(k.private[:])
	k.private[0] &= 248
	k.private[31] = (k.private[31] & 127) | 64
	pub, _ := curve25519.X25519(k.private[:], curve25519.Basepoint)
	copy(k.public[:], pub)
	return k
}

type peerConfig struct {
	address    netip.Addr
	listenPort int // 0 = random (a node only dials out)
	key        wgKey
	peerPublic [32]byte
	allowedIP  string // the hub's /32 on a node, the node's /32 on the hub
	endpoint   string // only on the node: where the hub is
}

func startPeer(c peerConfig) (*device.Device, *netstack.Net, error) {
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{c.address}, nil, 1420)
	if err != nil {
		return nil, nil, err
	}
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "wg: "))
	uapi := fmt.Sprintf("private_key=%s\nlisten_port=%d\npublic_key=%s\nallowed_ip=%s\n",
		hex.EncodeToString(c.key.private[:]), c.listenPort, hex.EncodeToString(c.peerPublic[:]), c.allowedIP)
	if c.endpoint != "" {
		uapi += fmt.Sprintf("endpoint=%s\n", c.endpoint)
	}
	if err := dev.IpcSet(uapi); err != nil {
		return nil, nil, err
	}
	if err := dev.Up(); err != nil {
		return nil, nil, err
	}
	if c.endpoint != "" {
		// The hub learns the node's address from the node's first packet. Setting the keepalive on
		// a running device sends one right away, so the node is reachable without waiting.
		err = dev.IpcSet(fmt.Sprintf("public_key=%s\npersistent_keepalive_interval=25\n",
			hex.EncodeToString(c.peerPublic[:])))
	}
	return dev, tnet, err
}
