// Package proxy serves a "proxy" service: the entity's own API behind the node. The node adds
// the channel (tunnel, mTLS with the gateway, logs without content) and, if configured, the
// local credential the internal API expects. That credential never leaves the node.
package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

// dropHeaders never reach the internal API: credentials of the consumer, and headers that a
// backend behind its own reverse proxy may trust to identify the user or rewrite the route.
var dropHeaders = []string{"Authorization", "Cookie", "Forwarded", "X-Real-Ip", "X-Remote-User",
	"X-Forwarded-User", "X-Original-Url", "X-Rewrite-Url"}

// New builds the handler of one proxy service. It receives requests with servicePath already
// stripped (server.NewHandler) and forwards them to the upstream's base path.
func New(servicePath string, spec config.ProxySpec) (http.Handler, error) {
	target, err := url.Parse(spec.Upstream)
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", spec.Upstream, err)
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if spec.CAFile != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(spec.CAFile)
		if err != nil || !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("caFile %s: no se pudo leer un certificado PEM", spec.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	setCredential := func(*http.Request) {}
	if c := spec.Credential; c != nil {
		switch {
		case c.Header != "":
			value, err := config.ReadSecret(c.File)
			if err != nil {
				return nil, err
			}
			setCredential = func(r *http.Request) { r.Header.Set(c.Header, value) }
		case c.Basic != nil:
			password, err := config.ReadSecret(c.Basic.PasswordFile)
			if err != nil {
				return nil, err
			}
			user := c.Basic.User
			setCredential = func(r *http.Request) { r.SetBasicAuth(user, password) }
		case c.ClientCert != "":
			pair, err := tls.LoadX509KeyPair(c.ClientCert, c.ClientKey)
			if err != nil {
				return nil, fmt.Errorf("certificado de cliente hacia el upstream: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{pair}
		}
	}
	templates, err := loadTemplates(spec.OpenAPI)
	if err != nil {
		return nil, err
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// The consumer's credentials stop at the node: the internal API sees only the node's.
			// Headers some backends trust for identity or routing are dropped too.
			for _, h := range dropHeaders {
				pr.Out.Header.Del(h)
			}
			setCredential(pr.Out)
		},
		Transport: &http.Transport{
			// Internal APIs are on the entity's LAN: no corporate proxy toward them.
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: spec.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSClientConfig:       tlsCfg,
			TLSHandshakeTimeout:   spec.ConnectTimeout,
			ResponseHeaderTimeout: spec.ResponseTimeout,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		FlushInterval: -1, // stream: documents can be large
		ErrorHandler:  errorHandler,
	}
	limit := spec.MaxRequestBytes
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := templates.match(r.URL.Path); t != "" {
			server.SetRoute(r, servicePath+t)
		}
		if r.ContentLength > limit {
			server.Problem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "El cuerpo de la petición supera el máximo permitido")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		rp.ServeHTTP(w, r)
	}), nil
}

// errorHandler answers upstream failures quickly and in Spanish. It never logs the error text:
// a *url.Error carries the URL, and the URL can carry an identifier.
func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	var netErr net.Error
	switch {
	case errors.As(err, &tooLarge):
		server.Problem(w, http.StatusRequestEntityTooLarge, "payload_too_large", "El cuerpo de la petición supera el máximo permitido")
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
		server.Problem(w, http.StatusGatewayTimeout, "upstream_timeout", "La API interna tardó demasiado en responder")
	default:
		server.Problem(w, http.StatusBadGateway, "bad_gateway", "La API interna no respondió")
	}
}

// Probe checks that the upstream accepts TCP connections (the heartbeat's health).
func Probe(ctx context.Context, spec config.ProxySpec) error {
	u, err := url.Parse(spec.Upstream)
	if err != nil {
		return err
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return fmt.Errorf("upstream %s no responde", u.Host)
	}
	return c.Close()
}
