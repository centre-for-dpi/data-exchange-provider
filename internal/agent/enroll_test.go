package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent/fakeportal"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *logBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func enroller(t *testing.T, p *fakeportal.Portal, token string) (*Enroller, *logBuffer) {
	t.Helper()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "enrollment_token")
	if token != "" {
		if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stateDir := filepath.Join(dir, "state")
	_ = os.Mkdir(stateDir, 0o700)
	logs := &logBuffer{}
	return &Enroller{
		Store: state.Store{Dir: stateDir}, HTTP: contract.NewPortalHTTPClient(p.PKI.Roots), Portal: p.URL(),
		TokenFile: tokenFile, KitVersion: "0.1.0-dev", ServicePort: 8443,
		Log: slog.New(slog.NewJSONHandler(logs, nil)), TokenPoll: 10 * time.Millisecond, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond,
	}, logs
}

func TestCSRShape(t *testing.T) {
	keyPEM, csrPEM, err := NewKeyAndCSR()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if csr.Subject.CommonName != "rid-node" || len(csr.DNSNames) != 0 || len(csr.IPAddresses) != 0 || !ok || pub.Curve != elliptic.P256() {
		t.Fatalf("CSR: CN=%q DNS=%v IP=%v key=%T", csr.Subject.CommonName, csr.DNSNames, csr.IPAddresses, csr.PublicKey)
	}
	if !strings.Contains(keyPEM, "PRIVATE KEY") {
		t.Fatal("key is not PEM")
	}
}

func TestEnrollPersistsState(t *testing.T) {
	p := fakeportal.New(t)
	e, logs := enroller(t, p, p.Token)
	st, err := e.LoadOrEnroll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.NodeID != p.NodeID || saved.NodeName != p.NodeName || saved.GatewayClientName != "rid-gateway.salud" ||
		saved.KeyPEM == "" || saved.WGPrivateKey == "" || saved.WireGuard.Address != "10.77.0.2/32" || saved.ControlURL != p.ControlURL() {
		t.Fatalf("state: %+v", saved)
	}
	var req contract.EnrollRequest
	_ = json.Unmarshal(p.Requests()[0].Body, &req)
	priv, _ := tunnel.ParseKey(saved.WGPrivateKey)
	if req.KitVersion != "0.1.0-dev" || req.ServicePort != 8443 || req.WireGuardPublicKey != tunnel.PublicKey(priv).String() {
		t.Fatalf("request: %+v", req)
	}
	if !strings.Contains(logs.String(), "enrollment_token") || strings.Contains(logs.String(), p.Token) {
		t.Fatalf("the log must say the token secret can go, and never print it: %s", logs)
	}
}

func TestExistingStateSkipsEnroll(t *testing.T) {
	p := fakeportal.New(t)
	e, _ := enroller(t, p, p.Token)
	if err := e.Store.Save(&state.State{NodeID: "already"}); err != nil {
		t.Fatal(err)
	}
	st, err := e.LoadOrEnroll(context.Background())
	if err != nil || st.NodeID != "already" || len(p.Requests()) != 0 {
		t.Fatalf("st=%+v err=%v requests=%d", st, err, len(p.Requests()))
	}
}

func TestCorruptStateDoesNotEnroll(t *testing.T) {
	p := fakeportal.New(t)
	e, _ := enroller(t, p, p.Token)
	_ = os.WriteFile(filepath.Join(e.Store.Dir, "state.json"), []byte("{"), 0o600)
	if _, err := e.LoadOrEnroll(context.Background()); err == nil || len(p.Requests()) != 0 {
		t.Fatalf("err=%v requests=%d", err, len(p.Requests()))
	}
}

func TestWaitsForToken(t *testing.T) {
	p := fakeportal.New(t)
	e, logs := enroller(t, p, "")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(e.TokenFile, []byte(p.Token), 0o600)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.LoadOrEnroll(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "esperando el token") {
		t.Fatalf("log: %s", logs)
	}
}

func TestWaitForTokenStopsWithContext(t *testing.T) {
	p := fakeportal.New(t)
	e, _ := enroller(t, p, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.LoadOrEnroll(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestEnroll401Fatal(t *testing.T) {
	p := fakeportal.New(t)
	e, _ := enroller(t, p, "tok_wrong_wrong_wrong_wrong")
	_, err := e.LoadOrEnroll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pide uno nuevo en el portal") || p.Count("/enroll") != 1 {
		t.Fatalf("err=%v calls=%d", err, p.Count("/enroll"))
	}
}

func TestEnroll409Fatal(t *testing.T) {
	p := fakeportal.New(t)
	p.EnrollStatuses = []int{409}
	e, _ := enroller(t, p, p.Token)
	_, err := e.LoadOrEnroll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "install.sh reenrolar") || p.Count("/enroll") != 1 {
		t.Fatalf("err=%v calls=%d", err, p.Count("/enroll"))
	}
}

func TestEnroll503Retried(t *testing.T) {
	p := fakeportal.New(t)
	p.EnrollStatuses = []int{503, 503}
	e, _ := enroller(t, p, p.Token)
	st, err := e.LoadOrEnroll(context.Background())
	if err != nil || st.NodeID != p.NodeID || p.Count("/enroll") != 3 {
		t.Fatalf("err=%v calls=%d", err, p.Count("/enroll"))
	}
}
