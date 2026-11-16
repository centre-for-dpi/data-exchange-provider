package contract

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/centre-for-dpi/data-exchange-provider/internal/testpki"
)

func TestHeartbeatUsesClientCertAndServerName(t *testing.T) {
	pki := testpki.New("rid-node-x", "rid-gateway.salud")
	nodeCert := pki.CA.Issue("rid-node", []string{"rid-node-x"}, x509.ExtKeyUsageClientAuth)
	var gotPath, gotCN, gotSNI string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCN, gotSNI = r.URL.Path, r.TLS.PeerCertificates[0].Subject.CommonName, r.TLS.ServerName
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{Status: "online", NextHeartbeatSeconds: 60})
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.CA.Issue("gateway", []string{"localhost"}, x509.ExtKeyUsageServerAuth)},
		ClientAuth:   tls.RequireAndVerifyClientCert, ClientCAs: pki.Roots,
	}
	srv.StartTLS()
	defer srv.Close()

	// The control URL names "localhost" (as in development); we dial the test server's address.
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	ctl := NewControl("https://localhost:"+port, srv.Listener.Addr().String(), pki.Roots, func() *tls.Certificate { return &nodeCert })
	res, err := ctl.Heartbeat(context.Background(), "abc", HeartbeatRequest{KitVersion: "0.1.0", ServicePort: 8443, Services: []ServiceInfo{}, Health: Health{Status: "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "online" || gotPath != "/api/v1/nodes/abc/heartbeat" || gotCN != "rid-node" || gotSNI != "localhost" {
		t.Fatalf("res=%+v path=%s cn=%s sni=%s", res, gotPath, gotCN, gotSNI)
	}
}

func TestProblemBecomesAPIError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"about:blank","title":"Unauthenticated","status":401,"code":"unauthenticated"}`)
	}))
	defer srv.Close()
	_, err := Enroll(context.Background(), srv.Client(), srv.URL, EnrollRequest{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || StatusOf(err) != 401 || apiErr.Code != "unauthenticated" {
		t.Fatalf("got %v (status %d)", err, StatusOf(err))
	}
	if StatusOf(errors.New("network")) != 0 {
		t.Fatal("StatusOf of a non-API error must be 0")
	}
}

func TestEnrollSendsBodyAndExpects201(t *testing.T) {
	var got EnrollRequest
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/nodes/enroll" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(EnrollResponse{NodeID: "n1"})
	}))
	defer srv.Close()
	res, err := Enroll(context.Background(), srv.Client(), srv.URL+"/", EnrollRequest{Token: "t", KitVersion: "0.1.0"})
	if err != nil || res.NodeID != "n1" || got.Token != "t" {
		t.Fatalf("res=%+v err=%v got=%+v", res, err, got)
	}
}

// A corporate proxy: the node's HTTPS to the portal goes through CONNECT, TLS end to end.
func TestPortalClientHonoursHTTPSProxy(t *testing.T) {
	portal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(EnrollResponse{NodeID: "via-proxy"})
	}))
	defer portal.Close()
	var connects atomic.Int32
	proxyLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyLn.Close()
	go func() {
		for {
			c, err := proxyLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				connects.Add(1)
				// example.com is in httptest's certificate; send it to the test server.
				up, err := net.Dial("tcp", portal.Listener.Addr().String())
				if err != nil {
					return
				}
				defer up.Close()
				_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}(c)
		}
	}()
	t.Setenv("HTTPS_PROXY", "http://"+proxyLn.Addr().String())
	t.Setenv("NO_PROXY", "")
	roots := x509.NewCertPool()
	roots.AddCert(portal.Certificate())
	_, port, _ := net.SplitHostPort(portal.Listener.Addr().String())
	res, err := Enroll(context.Background(), NewPortalHTTPClient(roots), "https://example.com:"+port, EnrollRequest{})
	if err != nil || res.NodeID != "via-proxy" || connects.Load() != 1 {
		t.Fatalf("res=%+v err=%v connects=%d", res, err, connects.Load())
	}
	if !strings.HasPrefix(portal.URL, "https://") {
		t.Fatal("portal must be https")
	}
}
