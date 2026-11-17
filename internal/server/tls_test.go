package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/testpki"
)

const (
	nodeName          = "den-node-01a1e5a6-9381-738c-a707-d6e29330ea26"
	gatewayClientName = "den-gateway.salud"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// serve starts the node's TLS server on loopback and returns its address and log.
func serve(t *testing.T, pki *testpki.PKI, live *atomic.Pointer[tls.Certificate]) (string, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if live == nil {
		live = &atomic.Pointer[tls.Certificate]{}
		live.Store(&pki.Node)
	}
	id := Identity{NodeName: nodeName, GatewayClientName: gatewayClientName, Roots: pki.Roots, Certificate: live.Load}
	h := NewHandler(nodeName, []Route{{Path: "/svc", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}}, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, l, TLSConfig(id), h, log) }()
	return l.Addr().String(), logs
}

// call makes one request as a client presenting cert (nil: none) with the given SNI.
func call(addr, sni string, roots *x509.CertPool, cert *tls.Certificate, skipVerify bool) (*http.Response, error) {
	cfg := &tls.Config{RootCAs: roots, ServerName: sni, InsecureSkipVerify: skipVerify, MinVersion: tls.VersionTLS13}
	if cert != nil {
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return cert, nil }
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true,
		DialContext: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, n, addr)
		}}}
	req, _ := http.NewRequest("GET", "https://"+nodeName+":8443/svc/x", nil)
	return c.Do(req)
}

func TestGatewayAccepted(t *testing.T) {
	pki := testpki.New(nodeName, gatewayClientName)
	addr, _ := serve(t, pki, nil)
	res, err := call(addr, nodeName, pki.Roots, &pki.Gateway, false)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if body, _ := io.ReadAll(res.Body); res.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func rejected(t *testing.T, addr string, logs *syncBuffer, wantReason string, do func() (*http.Response, error)) {
	t.Helper()
	res, err := do()
	if err == nil {
		res.Body.Close()
		t.Fatalf("expected a TLS rejection, got HTTP %d", res.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), wantReason) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), `"msg":"tls_rejected"`) || !strings.Contains(logs.String(), wantReason) {
		t.Fatalf("log lacks tls_rejected with %q:\n%s", wantReason, logs)
	}
}

func TestConsumerAppSameCARejected(t *testing.T) {
	pki := testpki.New(nodeName, gatewayClientName)
	addr, logs := serve(t, pki, nil)
	rejected(t, addr, logs, "is not the gateway", func() (*http.Response, error) {
		return call(addr, nodeName, pki.Roots, &pki.ConsumerApp, false)
	})
}

func TestForeignCARejected(t *testing.T) {
	pki := testpki.New(nodeName, gatewayClientName)
	addr, logs := serve(t, pki, nil)
	rejected(t, addr, logs, "unknown authority", func() (*http.Response, error) {
		return call(addr, nodeName, pki.Roots, &pki.ForeignGateway, false)
	})
}

func TestNoClientCertRejected(t *testing.T) {
	pki := testpki.New(nodeName, gatewayClientName)
	addr, logs := serve(t, pki, nil)
	rejected(t, addr, logs, "certificate", func() (*http.Response, error) {
		return call(addr, nodeName, pki.Roots, nil, false)
	})
}

func TestWrongSNIRejected(t *testing.T) {
	pki := testpki.New(nodeName, gatewayClientName)
	addr, logs := serve(t, pki, nil)
	rejected(t, addr, logs, "is not this node", func() (*http.Response, error) {
		return call(addr, "den-node-otro", pki.Roots, &pki.Gateway, true)
	})
}

func TestRenewedCertificateServed(t *testing.T) {
	pki := testpki.New(nodeName, gatewayClientName)
	live := &atomic.Pointer[tls.Certificate]{}
	live.Store(&pki.Node)
	addr, _ := serve(t, pki, live)
	renewed := pki.CA.Issue("den-node", []string{nodeName}, x509.ExtKeyUsageServerAuth)
	live.Store(&renewed)
	res, err := call(addr, nodeName, pki.Roots, &pki.Gateway, false)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	want, _ := x509.ParseCertificate(renewed.Certificate[0])
	if got := res.TLS.PeerCertificates[0].SerialNumber; got.Cmp(want.SerialNumber) != 0 {
		t.Fatalf("served serial %v, want the renewed %v", got, want.SerialNumber)
	}
}
