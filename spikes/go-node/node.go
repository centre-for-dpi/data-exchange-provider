// THROWAWAY SPIKE. The node's data plane: TLS with the node certificate, accepting only the
// gateway's client certificate (network CA + exact name, checked in the handshake), routing by
// servicePath to either an instant API or the entity's own API, and metadata-only logs.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type nodeConfig struct {
	nodeName          string // rid-node-<nodeId>: SNI, Host and the node certificate's DNS name
	gatewayClientName string // the only client certificate name accepted
	cert              tls.Certificate
	caPool            *x509.CertPool
	current           func() *tls.Certificate // optional: the live certificate after renewals
}

func nodeTLSConfig(c nodeConfig) *tls.Config {
	current := c.current
	if current == nil {
		current = func() *tls.Certificate { return &c.cert }
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return current(), nil
		},
		ClientAuth:   tls.RequireAndVerifyClientCert, // chain to the network CA, clientAuth usage
		ClientCAs:    c.caPool,
		// Runs after chain verification and before any HTTP byte: a wrong name aborts the handshake.
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.ServerName != c.nodeName {
				return fmt.Errorf("SNI %q is not this node", cs.ServerName)
			}
			if cn := cs.PeerCertificates[0].Subject.CommonName; cn != c.gatewayClientName {
				return fmt.Errorf("client certificate %q is not the gateway", cn)
			}
			return nil
		},
	}
}

// ---------- routing ----------

type routeKey struct{}

// setRoute records the route TEMPLATE for the access log; the real path never gets logged.
func setRoute(r *http.Request, template string) {
	if p, ok := r.Context().Value(routeKey{}).(*string); ok {
		*p = template
	}
}

type service struct {
	path    string // servicePath, e.g. /salud-afiliacion
	handler http.Handler
}

func ownAPIProxy(servicePath, backend, credentialHeader, credential string) service {
	target, _ := url.Parse(backend)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			if credentialHeader != "" { // the entity's own API credential: stays inside the node
				pr.Out.Header.Set(credentialHeader, credential)
			}
		},
		ErrorLog: log.New(os.Stderr, "proxy: ", 0),
	}
	return service{servicePath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Own APIs: the node does not know their templates in the spike, only the service.
		setRoute(r, servicePath+"/*")
		proxy.ServeHTTP(w, r)
	})}
}

func router(nodeName string, services []service) http.Handler {
	mux := http.NewServeMux()
	for _, s := range services {
		mux.Handle(s.path+"/", http.StripPrefix(s.path, s.handler))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		setRoute(r, "(no service)")
		problem(w, 404, "not_found", "Recurso no encontrado")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := strings.Cut(r.Host, ":"); host != nodeName {
			setRoute(r, "(wrong host)")
			problem(w, 421, "misdirected_request", "Host incorrecto")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ---------- access log: metadata only ----------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		route := "(unknown)"
		r = r.WithContext(context.WithValue(r.Context(), routeKey{}, &route))
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		client := ""
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			client = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		// Which network headers arrived: names only, never their values.
		var ridHeaders []string
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-rid-") {
				ridHeaders = append(ridHeaders, strings.ToLower(name))
			}
		}
		sort.Strings(ridHeaders)
		line, _ := json.Marshal(map[string]any{
			"ts": start.UTC().Format(time.RFC3339Nano), "request_id": r.Header.Get("X-Request-Id"),
			"method": r.Method, "route": route, "status": rec.status,
			"ms": time.Since(start).Milliseconds(), "client": client,
			"query_present": r.URL.RawQuery != "", "rid_headers": ridHeaders,
		})
		fmt.Fprintln(os.Stderr, "access:", string(line))
	})
}

func serveNode(l net.Listener, c nodeConfig, services []service) *http.Server {
	srv := &http.Server{
		Handler:           accessLog(router(c.nodeName, services)),
		TLSConfig:         nodeTLSConfig(c),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(os.Stderr, "tls: ", 0), // handshake rejections land here
	}
	go func() { _ = srv.ServeTLS(l, "", "") }()
	return srv
}
