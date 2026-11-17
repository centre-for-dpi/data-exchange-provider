package contract

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// APIError is a non-2xx answer from the portal (RFC 9457 problem details).
type APIError struct {
	Status int
	Code   string
	Title  string
}

func (e *APIError) Error() string {
	return i18n.Sprintf("the portal answered %d %s: %s", e.Status, e.Code, e.Title)
}

// StatusOf returns the HTTP status of an APIError, or 0 for any other error.
func StatusOf(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

// proxyFromEnvironment reads HTTPS_PROXY/NO_PROXY on every request (net/http caches them once
// per process), so a corporate proxy configured in the container's environment is honoured.
func proxyFromEnvironment(r *http.Request) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(r.URL)
}

// NewPortalHTTPClient is the client for enrollment: the portal's public HTTPS, through the
// corporate proxy if there is one. roots nil means the system's roots.
func NewPortalHTTPClient(roots *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:               proxyFromEnvironment,
			TLSClientConfig:     &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// Enroll sends the single-use token, the CSR and the WireGuard public key (expects 201).
func Enroll(ctx context.Context, hc *http.Client, portalURL string, req EnrollRequest) (*EnrollResponse, error) {
	var res EnrollResponse
	err := do(ctx, hc, http.MethodPost, strings.TrimRight(portalURL, "/")+"/api/v1/nodes/enroll", req, http.StatusCreated, &res)
	return &res, err
}

// Control is the control channel: TLS with the node certificate to the gateway, which forwards
// to the portal (contract v1).
type Control struct {
	base string
	hc   *http.Client
}

// NewControl builds the control client. dialAddress (development only) replaces the host
// dialed; the TLS name stays the controlURL's. cert returns the live node certificate, so a
// renewal takes effect on the next request.
func NewControl(controlURL, dialAddress string, roots *x509.CertPool, cert func() *tls.Certificate) *Control {
	u, _ := url.Parse(controlURL)
	tr := &http.Transport{
		Proxy: proxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			RootCAs: roots, ServerName: u.Hostname(), MinVersion: tls.VersionTLS12,
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return cert(), nil },
		},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if dialAddress != "" {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, dialAddress)
		}
	}
	return &Control{base: strings.TrimRight(controlURL, "/") + "/api/v1/nodes/", hc: &http.Client{Timeout: 30 * time.Second, Transport: tr}}
}

func (c *Control) url(nodeID, op string) string { return c.base + url.PathEscape(nodeID) + "/" + op }

// Heartbeat sends version, services and health.
func (c *Control) Heartbeat(ctx context.Context, nodeID string, r HeartbeatRequest) (*HeartbeatResponse, error) {
	var res HeartbeatResponse
	return &res, do(ctx, c.hc, http.MethodPost, c.url(nodeID, "heartbeat"), r, http.StatusOK, &res)
}

// Renew asks for a certificate for a new key.
func (c *Control) Renew(ctx context.Context, nodeID, csrPEM string) (*RenewResponse, error) {
	var res RenewResponse
	return &res, do(ctx, c.hc, http.MethodPost, c.url(nodeID, "renew"), RenewRequest{CSR: csrPEM}, http.StatusOK, &res)
}

// Config returns what the network expects from the node.
func (c *Control) Config(ctx context.Context, nodeID string) (*ConfigResponse, error) {
	var res ConfigResponse
	return &res, do(ctx, c.hc, http.MethodGet, c.url(nodeID, "config"), nil, http.StatusOK, &res)
}

func do(ctx context.Context, hc *http.Client, method, target string, body any, want int, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		apiErr := &APIError{Status: resp.StatusCode}
		var p struct{ Code, Title string }
		if json.Unmarshal(raw, &p) == nil {
			apiErr.Code, apiErr.Title = p.Code, p.Title
		}
		return apiErr
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return i18n.Errorf("unreadable answer from the portal: %w", err)
	}
	return nil
}
