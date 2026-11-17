package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func sample() *State {
	return &State{
		NodeID: "01a1e5a6-9381-738c-a707-d6e29330ea26", NodeName: "rid-node-01a1e5a6-9381-738c-a707-d6e29330ea26",
		Certificate: "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n", CertificateNotAfter: "2027-02-14T18:37:54Z",
		CACertificate: "ca", ControlURL: "https://localhost:9443", GatewayClientName: "rid-gateway.salud",
		HeartbeatIntervalSeconds: 60,
		WireGuard: WireGuard{Address: "10.77.0.19/32", HubPublicKey: "hub", HubEndpoint: "wg-hub:51820",
			HubAddress: "10.77.0.1", AllowedIPs: []string{"10.77.0.1/32"}, PersistentKeepaliveSeconds: 25},
		KeyPEM: "key", WGPrivateKey: "wg",
		PendingRenewal: &PendingRenewal{KeyPEM: "k2", CSRPEM: "csr", CreatedAt: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	want := sample()
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != want.NodeID || got.WireGuard.AllowedIPs[0] != "10.77.0.1/32" ||
		got.PendingRenewal == nil || got.PendingRenewal.CSRPEM != "csr" || !got.PendingRenewal.CreatedAt.Equal(want.PendingRenewal.CreatedAt) {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for i := 0; i < 3; i++ {
		if err := s.Save(sample()); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Fatalf("leftover file %s", e.Name())
		}
	}
}

func TestStateFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	s := Store{Dir: t.TempDir()}
	if err := s.Save(sample()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadMissingIsErrNoState(t *testing.T) {
	if _, err := (Store{Dir: t.TempDir()}).Load(); !errors.Is(err, ErrNoState) {
		t.Fatalf("got %v, want ErrNoState", err)
	}
}

func TestLoadCorruptIsNotErrNoState(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(s.Dir, "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	if err == nil || errors.Is(err, ErrNoState) {
		t.Fatalf("a corrupt state must be an error other than ErrNoState, got %v", err)
	}
	if !strings.Contains(err.Error(), "reenroll") {
		t.Fatalf("the error must point to install.sh reenroll: %v", err)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	type status struct{ PortalState string }
	if err := s.WriteStatus(status{PortalState: "online"}); err != nil {
		t.Fatal(err)
	}
	var got status
	if err := s.ReadStatus(&got); err != nil || got.PortalState != "online" {
		t.Fatalf("ReadStatus = %+v, %v", got, err)
	}
}
