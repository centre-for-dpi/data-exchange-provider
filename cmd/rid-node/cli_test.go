package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/agent/fakeportal"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel/tunneltest"
)

func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// portalWithDate serves /api/v1/health with a Date header shifted by skew.
func portalWithDate(t *testing.T, skew time.Duration) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().Add(skew).UTC().Format(http.TimeFormat))
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doctorFor(t *testing.T, portal *httptest.Server, extraYAML string, statusFile *agent.Status) (int, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := writeFile(t, dir, "node.yaml", "portal: "+portal.URL+"\n"+extraYAML, 0o600)
	stateDir := filepath.Join(dir, "state")
	_ = os.Mkdir(stateDir, 0o700)
	if statusFile != nil {
		if err := (state.Store{Dir: stateDir}).WriteStatus(statusFile); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	code := doctor(context.Background(), doctorOptions{ConfigPath: cfg, StateDir: stateDir, TokenFile: filepath.Join(dir, "token"),
		PortalHTTP: portal.Client(), Now: time.Now}, &out)
	return code, out.String()
}

func TestDoctorHealthy(t *testing.T) {
	code, out := doctorFor(t, portalWithDate(t, 0), "", nil)
	if code != 0 || strings.Contains(out, "✘") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDoctorClockSkew(t *testing.T) {
	code, out := doctorFor(t, portalWithDate(t, 5*time.Minute), "", nil)
	if code != 1 || !strings.Contains(out, "✘") || !strings.Contains(out, "reloj") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDoctorSecretPermsWarn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	cred := writeFile(t, dir, "cred", "s3cr3t", 0o644)
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer up.Close()
	extra := fmt.Sprintf("services:\n  - { name: certificados, path: /certificados, type: proxy, upstream: %s, allowPlainHttp: true, credential: { header: X-Api-Key, file: %s } }\n", up.URL, cred)
	_, out := doctorFor(t, portalWithDate(t, 0), extra, nil)
	if !strings.Contains(out, "⚠") || !strings.Contains(out, cred) {
		t.Fatalf("%s", out)
	}
}

func TestDoctorStaleHandshake(t *testing.T) {
	st := &agent.Status{NodeName: "rid-node-x", PortalState: "online", LastHandshake: time.Now().Add(-10 * time.Minute), LastHeartbeat: time.Now()}
	code, out := doctorFor(t, portalWithDate(t, 0), "", st)
	if code != 1 || !strings.Contains(out, "UDP 51820") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDoctorUpstreamDown(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	extra := "services:\n  - { name: certificados, path: /certificados, type: proxy, upstream: http://" + addr + ", allowPlainHttp: true }\n"
	code, out := doctorFor(t, portalWithDate(t, 0), extra, nil)
	if code != 1 || !strings.Contains(out, "certificados") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestStatusHuman(t *testing.T) {
	dir := t.TempDir()
	st := agent.Status{NodeName: "rid-node-abc", PortalState: "online", LastHeartbeat: time.Now().Add(-12 * time.Second),
		LastHandshake: time.Now().Add(-30 * time.Second), CertificateNotAfter: "2027-02-14T00:00:00Z",
		Health:   contract.Health{Status: "degraded", Detail: "sin respuesta: afiliacion"},
		Services: []agent.ServiceStatus{{Name: "certificados", Path: "/certificados", Healthy: true}, {Name: "afiliacion", Path: "/salud", Healthy: false, Detail: "upstream no responde"}},
		Notices:  []string{"actualiza el kit"}, KitVersion: "0.1.0-dev", Variant: "canal"}
	if err := (state.Store{Dir: dir}).WriteStatus(st); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := status(dir, false, &out, time.Now); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"rid-node-abc", "online", "2027-02-14", "certificados", "upstream no responde", "actualiza el kit", "canal"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status lacks %q:\n%s", want, out.String())
		}
	}
}

func TestStatusWithoutFile(t *testing.T) {
	var out bytes.Buffer
	if code := status(t.TempDir(), false, &out, time.Now); code != 1 || !strings.Contains(out.String(), "todavía") {
		t.Fatalf("code=%d %s", code, out.String())
	}
}

// The whole node in process: enroll with the fake portal, tunnel to a test hub, serve a proxy
// service, heartbeat; then the "gateway" calls through the hub; then a clean stop.
func TestRunEndToEndInProcess(t *testing.T) {
	p := fakeportal.New(t)
	nodeAddr := netip.MustParsePrefix("10.77.0.2/32")
	var dial func(context.Context, netip.AddrPort) (net.Conn, error)
	p.OnEnroll = func(req contract.EnrollRequest) contract.WireGuard {
		nodePub, _ := tunnel.ParseKey(req.WireGuardPublicKey)
		var hubPub tunnel.Key
		var endpoint string
		hubPub, endpoint, dial = tunneltest.Hub(t, nodePub, nodeAddr)
		return contract.WireGuard{Address: nodeAddr.String(), HubPublicKey: hubPub.String(), HubEndpoint: endpoint,
			HubAddress: tunneltest.HubAddress.String(), AllowedIPs: []string{tunneltest.HubAddress.String() + "/32"}, PersistentKeepaliveSeconds: 25}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "upstream:"+r.URL.Path+":"+r.Header.Get("X-RID-Purpose"))
	}))
	defer upstream.Close()
	dir := t.TempDir()
	cfg := writeFile(t, dir, "node.yaml", fmt.Sprintf(`portal: %s
controlDialAddress: %s
services:
  - { name: certificados, path: /certificados, type: proxy, upstream: %s/base, allowPlainHttp: true }
`, p.URL(), p.DialAddress(), upstream.URL), 0o600)
	token := writeFile(t, dir, "token", p.Token, 0o600)
	stateDir := filepath.Join(dir, "state")
	_ = os.Mkdir(stateDir, 0o700)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runNode(ctx, runOptions{ConfigPath: cfg, StateDir: stateDir, TokenFile: token, PortalRoots: p.PKI.Roots,
			HeartbeatEvery: 50 * time.Millisecond, Log: slog.New(slog.DiscardHandler)})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for p.Count("/heartbeat") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p.Count("/heartbeat") == 0 {
		t.Fatal("the node never sent a heartbeat")
	}
	gw := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{ServerName: p.NodeName, RootCAs: p.PKI.Roots, Certificates: []tls.Certificate{p.PKI.Gateway}},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, netip.AddrPortFrom(nodeAddr.Addr(), 8443))
		},
	}}
	req, _ := http.NewRequest("GET", "https://"+p.NodeName+":8443/certificados/v1/abc", nil)
	req.Header.Set("X-RID-Purpose", "eligibility_check")
	res, err := gw.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "upstream:/base/v1/abc:eligibility_check" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	// status.json is written after the heartbeat's answer is processed: wait for it.
	var st agent.Status
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err := (state.Store{Dir: stateDir}).ReadStatus(&st); err == nil && st.PortalState == "online" {
			break
		}
	}
	if st.PortalState != "online" {
		t.Fatalf("status %+v", st)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runNode: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runNode did not stop")
	}
}
