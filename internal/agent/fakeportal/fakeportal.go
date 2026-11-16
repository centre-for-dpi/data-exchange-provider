// Package fakeportal is the portal's side of the node contract v1, for tests only: enrollment
// and the control channel (heartbeat, renew, config) on one TLS server whose CA signs node CSRs.
// Responses can be scripted per call. Production code never imports it.
package fakeportal

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/testpki"
)

// Request is one call the portal received.
type Request struct {
	Method, Path string
	Body         []byte
	ClientCN     string
}

// Portal is a scriptable fake. Zero values of the script fields mean "answer normally".
type Portal struct {
	PKI               *testpki.PKI
	Token             string
	NodeID, NodeName  string
	GatewayClientName string
	WireGuard         contract.WireGuard
	Server            *httptest.Server

	mu              sync.Mutex
	requests        []Request
	EnrollStatuses  []int // consumed one per enroll call; 201 when empty
	Heartbeat       contract.HeartbeatResponse
	HeartbeatStatus int  // non-zero: answer heartbeats with this status (problem+json)
	Down            bool // drop every control connection without answering
	RenewDrops      int  // drop this many renew calls after signing (the response is lost)
	RenewStatus     int  // non-zero: refuse renewals with this status
	Config          contract.ConfigResponse
	// OnEnroll, if set, returns the tunnel configuration for the enrolling node (tests start a
	// hub that knows the node's WireGuard public key).
	OnEnroll func(req contract.EnrollRequest) contract.WireGuard
}

// New starts the fake portal.
func New(t testing.TB) *Portal {
	t.Helper()
	const id = "01a1e5a6-9381-738c-a707-d6e29330ea26"
	p := &Portal{
		Token: "tok_0123456789abcdefghij", NodeID: id, NodeName: "rid-node-" + id,
		GatewayClientName: "rid-gateway.salud",
		WireGuard: contract.WireGuard{Address: "10.77.0.2/32", HubPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			HubEndpoint: "127.0.0.1:1", HubAddress: "10.77.0.1", AllowedIPs: []string{"10.77.0.1/32"}, PersistentKeepaliveSeconds: 25},
		Heartbeat: contract.HeartbeatResponse{Status: "online", NextHeartbeatSeconds: 60},
	}
	p.PKI = testpki.New(p.NodeName, p.GatewayClientName)
	p.Config = contract.ConfigResponse{ContractVersion: "1", NodeID: id, HeartbeatIntervalSeconds: 60, Services: []contract.PublishedService{}, Notices: []contract.Notice{}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(p.serve))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{p.PKI.CA.Issue("portal", []string{"localhost", "127.0.0.1"}, x509.ExtKeyUsageServerAuth)},
		ClientAuth:   tls.VerifyClientCertIfGiven, ClientCAs: p.PKI.Roots,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	p.Server = srv
	return p
}

// URL is the portal's enrollment URL.
func (p *Portal) URL() string { return "https://" + p.Server.Listener.Addr().String() }

// ControlURL is what enrollment hands out: localhost, as in development.
func (p *Portal) ControlURL() string {
	_, port, _ := net.SplitHostPort(p.Server.Listener.Addr().String())
	return "https://localhost:" + port
}

// DialAddress is where ControlURL really is.
func (p *Portal) DialAddress() string { return p.Server.Listener.Addr().String() }

// Requests returns a copy of the calls received.
func (p *Portal) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

// Count returns how many calls hit a path suffix (e.g. "/heartbeat").
func (p *Portal) Count(suffix string) int {
	n := 0
	for _, r := range p.Requests() {
		if strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

// Set changes the script under the lock.
func (p *Portal) Set(f func(p *Portal)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"about:blank","title":"%s","status":%d,"code":"%s"}`, code, status, code)
}

func drop(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if c, _, err := hj.Hijack(); err == nil {
			c.Close()
		}
	}
}

func (p *Portal) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	cn := ""
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		cn = r.TLS.PeerCertificates[0].Subject.CommonName
	}
	p.mu.Lock()
	p.requests = append(p.requests, Request{r.Method, r.URL.Path, body, cn})
	p.mu.Unlock()

	if r.URL.Path == "/api/v1/nodes/enroll" {
		p.enroll(w, body)
		return
	}
	prefix := "/api/v1/nodes/" + p.NodeID + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		problem(w, http.StatusNotFound, "not_found")
		return
	}
	p.mu.Lock()
	down := p.Down
	p.mu.Unlock()
	if down {
		drop(w)
		return
	}
	if cn != "rid-node" {
		problem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	switch strings.TrimPrefix(r.URL.Path, prefix) {
	case "heartbeat":
		p.mu.Lock()
		status, res := p.HeartbeatStatus, p.Heartbeat
		p.mu.Unlock()
		if status != 0 {
			problem(w, status, "refused")
			return
		}
		_ = json.NewEncoder(w).Encode(res)
	case "config":
		p.mu.Lock()
		res := p.Config
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(res)
	case "renew":
		p.renew(w, body)
	default:
		problem(w, http.StatusNotFound, "not_found")
	}
}

func (p *Portal) enroll(w http.ResponseWriter, body []byte) {
	p.mu.Lock()
	status := http.StatusCreated
	if len(p.EnrollStatuses) > 0 {
		status, p.EnrollStatuses = p.EnrollStatuses[0], p.EnrollStatuses[1:]
	}
	p.mu.Unlock()
	if status != http.StatusCreated {
		problem(w, status, "scripted")
		return
	}
	var req contract.EnrollRequest
	if json.Unmarshal(body, &req) != nil || req.Token != p.Token {
		problem(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	cert, err := p.PKI.CA.SignCSR(req.CSR, p.NodeName)
	if err != nil {
		problem(w, http.StatusBadRequest, "validation_failed")
		return
	}
	wg := p.WireGuard
	if p.OnEnroll != nil {
		wg = p.OnEnroll(req)
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(contract.EnrollResponse{
		ContractVersion: "1", NodeID: p.NodeID, NodeName: p.NodeName, Certificate: cert,
		CertificateNotAfter: "2027-02-14T00:00:00Z", CACertificate: p.PKI.CA.CertPEM(),
		ControlURL: p.ControlURL(), GatewayClientName: p.GatewayClientName,
		HeartbeatIntervalSeconds: 60, WireGuard: wg,
	})
}

func (p *Portal) renew(w http.ResponseWriter, body []byte) {
	var req contract.RenewRequest
	_ = json.Unmarshal(body, &req)
	p.mu.Lock()
	status := p.RenewStatus
	dropIt := p.RenewDrops > 0
	if dropIt {
		p.RenewDrops--
	}
	p.mu.Unlock()
	if status != 0 {
		problem(w, status, "refused")
		return
	}
	cert, err := p.PKI.CA.SignCSR(req.CSR, p.NodeName)
	if err != nil {
		problem(w, http.StatusBadRequest, "validation_failed")
		return
	}
	if dropIt {
		drop(w)
		return
	}
	_ = json.NewEncoder(w).Encode(contract.RenewResponse{Certificate: cert, CertificateNotAfter: "2027-05-14T00:00:00Z", PreviousValidUntil: "2026-11-17T00:00:00Z"})
}

// RenewCSRs returns the CSRs received by /renew, in order.
func (p *Portal) RenewCSRs() []string {
	var out []string
	for _, r := range p.Requests() {
		if strings.HasSuffix(r.Path, "/renew") {
			var req contract.RenewRequest
			_ = json.Unmarshal(r.Body, &req)
			out = append(out, req.CSR)
		}
	}
	return out
}
