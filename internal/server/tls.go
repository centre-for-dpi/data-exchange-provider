// Package server is the node's data plane: TLS that admits only the gateway, routing by
// servicePath, and an access log with metadata only.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Identity is who the node is and whom it accepts.
type Identity struct {
	NodeName          string // den-node-<nodeId>: SNI, Host and the node certificate's DNS name
	GatewayClientName string // the only client certificate name accepted (CN)
	Roots             *x509.CertPool
	Certificate       func() *tls.Certificate // the live node certificate (swapped on renewal)
}

// TLSConfig requires TLS 1.3 and a client certificate from the network's CA whose CN is exactly
// the gateway's client name, for this node's SNI. The check runs in the handshake, so an
// impostor never gets to send an HTTP byte.
func TLSConfig(id Identity) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return id.Certificate(), nil
		},
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  id.Roots,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.ServerName != id.NodeName {
				return fmt.Errorf("SNI %q is not this node", cs.ServerName)
			}
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no client certificate")
			}
			if cn := cs.PeerCertificates[0].Subject.CommonName; cn != id.GatewayClientName {
				return fmt.Errorf("client certificate %q is not the gateway", cn)
			}
			return nil
		},
	}
}

// Serve serves h over TLS on l until ctx ends. There is no write timeout: proxied responses
// stream (documents can be large).
func Serve(ctx context.Context, l net.Listener, cfg *tls.Config, h http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		TLSConfig:         cfg,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(serverErrors{logger}, "", 0),
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	err := srv.ServeTLS(l, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// serverErrors turns net/http's error log into structured events. Handshake failures are the
// rejected impostors; their reason is safe to log (names and CA problems, no request data).
type serverErrors struct{ log *slog.Logger }

func (s serverErrors) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if i := strings.Index(line, "TLS handshake error from "); i >= 0 {
		rest := line[i+len("TLS handshake error from "):]
		from, reason, _ := strings.Cut(rest, ": ")
		s.log.Warn("tls_rejected", "from", from, "reason", reason)
	} else {
		s.log.Warn("http_server", "detail", line)
	}
	return len(p), nil
}
