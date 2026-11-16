package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
	"github.com/centre-for-dpi/data-exchange-provider/internal/testpki"
)

const nodeName = "rid-node-test"

func secret(t *testing.T, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(upstream string) config.ProxySpec {
	return config.ProxySpec{Upstream: upstream, AllowPlainHTTP: true, MaxRequestBytes: config.DefaultMaxRequestBytes,
		ConnectTimeout: config.DefaultConnectTimeout, ResponseTimeout: config.DefaultResponseTimeout}
}

// through runs a request through the node's handler (as the server does) into the proxy.
func through(t *testing.T, s config.ProxySpec, method, target string, body io.Reader, header map[string]string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	h, err := New("/certificados", s)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	node := server.NewHandler(nodeName, []server.Route{{Path: "/certificados", Handler: h}}, slog.New(slog.NewJSONHandler(&logs, nil)))
	r := httptest.NewRequest(method, target, body)
	r.Host = nodeName
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	node.ServeHTTP(w, r)
	return w, logs.String()
}

type seen struct {
	Path, Query, Auth, APIKey, Cookie, Purpose, Host string
}

func echoUpstream(t *testing.T) (*httptest.Server, chan seen) {
	t.Helper()
	ch := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // keep the first request; never block the upstream on a test that ignores it
		case ch <- seen{r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("Cookie"), r.Header.Get("X-RID-Purpose"), r.Host}:
		default:
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

func TestRewritesPathAndKeepsQuery(t *testing.T) {
	up, ch := echoUpstream(t)
	w, _ := through(t, spec(up.URL+"/base"), "GET", "/certificados/v1/x?y=1", nil, nil)
	got := <-ch
	if w.Code != 200 || got.Path != "/base/v1/x" || got.Query != "y=1" || got.Host != strings.TrimPrefix(up.URL, "http://") {
		t.Fatalf("code=%d seen=%+v", w.Code, got)
	}
}

func TestInjectsHeaderCredentialAndDropsInboundAuthorization(t *testing.T) {
	up, ch := echoUpstream(t)
	s := spec(up.URL)
	s.Credential = &config.Credential{Header: "X-Api-Key", File: secret(t, "local-secret")}
	w, logs := through(t, s, "GET", "/certificados/1", nil, map[string]string{"Authorization": "Bearer consumer-token", "Cookie": "a=b"})
	got := <-ch
	if w.Code != 200 || got.APIKey != "local-secret" || got.Auth != "" || got.Cookie != "" {
		t.Fatalf("seen=%+v", got)
	}
	if strings.Contains(logs, "local-secret") {
		t.Fatal("the credential reached the log")
	}
}

func TestBasicCredential(t *testing.T) {
	up, ch := echoUpstream(t)
	s := spec(up.URL)
	s.Credential = &config.Credential{Basic: &config.Basic{User: "rid", PasswordFile: secret(t, "pw")}}
	through(t, s, "GET", "/certificados/1", nil, nil)
	if got := <-ch; got.Auth != "Basic cmlkOnB3" {
		t.Fatalf("auth %q", got.Auth)
	}
}

func TestForwardsRIDHeaders(t *testing.T) {
	up, ch := echoUpstream(t)
	through(t, spec(up.URL), "GET", "/certificados/1", nil, map[string]string{"X-RID-Purpose": "eligibility_check"})
	if got := <-ch; got.Purpose != "eligibility_check" {
		t.Fatalf("purpose %q", got.Purpose)
	}
}

// tlsUpstream is an HTTPS upstream with a certificate from an internal CA; requireClient makes
// it demand a client certificate from the same CA.
func tlsUpstream(t *testing.T, requireClient bool) (*httptest.Server, *testpki.PKI, string) {
	t.Helper()
	pki := testpki.New("unused", "unused")
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn := ""
		if len(r.TLS.PeerCertificates) > 0 {
			cn = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		_, _ = io.WriteString(w, "client="+cn)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pki.CA.Issue("api-interna", []string{"127.0.0.1"}, x509.ExtKeyUsageServerAuth)}}
	if requireClient {
		srv.TLS.ClientAuth, srv.TLS.ClientCAs = tls.RequireAndVerifyClientCert, pki.Roots
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	_ = os.WriteFile(caFile, []byte(pki.CA.CertPEM()), 0o644)
	return srv, pki, caFile
}

func TestUpstreamCustomCA(t *testing.T) {
	up, _, caFile := tlsUpstream(t, false)
	s := spec(up.URL)
	s.CAFile = caFile
	if w, _ := through(t, s, "GET", "/certificados/1", nil, nil); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s.CAFile = ""
	if w, _ := through(t, s, "GET", "/certificados/1", nil, nil); w.Code != http.StatusBadGateway {
		t.Fatalf("without the internal CA the upstream must not be trusted: %d", w.Code)
	}
}

func TestClientCertToUpstream(t *testing.T) {
	up, pki, caFile := tlsUpstream(t, true)
	cert := pki.CA.Issue("rid-nodo", nil, x509.ExtKeyUsageClientAuth)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	writePair(t, cert, certFile, keyFile)
	s := spec(up.URL)
	s.CAFile = caFile
	s.Credential = &config.Credential{ClientCert: certFile, ClientKey: keyFile}
	w, _ := through(t, s, "GET", "/certificados/1", nil, nil)
	if w.Code != 200 || w.Body.String() != "client=rid-nodo" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRequestBodyLimit413(t *testing.T) {
	up, _ := echoUpstream(t)
	s := spec(up.URL)
	s.MaxRequestBytes = 10
	w, _ := through(t, s, "POST", "/certificados/1", strings.NewReader(strings.Repeat("x", 11)), nil)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "payload_too_large") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestStreamsLargeResponse(t *testing.T) {
	big := bytes.Repeat([]byte("p"), 5<<20)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(big) }))
	defer up.Close()
	w, _ := through(t, spec(up.URL), "GET", "/certificados/doc.pdf", nil, nil)
	if w.Code != 200 || w.Body.Len() != len(big) {
		t.Fatalf("%d, %d bytes", w.Code, w.Body.Len())
	}
}

func TestUpstreamDown502(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	w, logs := through(t, spec("http://"+addr), "GET", "/certificados/1000000001", nil, nil)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "bad_gateway") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(logs, "1000000001") {
		t.Fatalf("the upstream error leaked the path into the log: %s", logs)
	}
}

func TestUpstreamSlow504(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer up.Close()
	s := spec(up.URL)
	s.ResponseTimeout = 200 * time.Millisecond
	start := time.Now()
	w, _ := through(t, s, "GET", "/certificados/1", nil, nil)
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "upstream_timeout") || time.Since(start) > time.Second {
		t.Fatalf("%d %s after %v", w.Code, w.Body, time.Since(start))
	}
}

func TestTemplateFromOpenAPI(t *testing.T) {
	up, _ := echoUpstream(t)
	doc := map[string]any{"openapi": "3.1.0", "paths": map[string]any{
		"/afiliados/{numeroIdentificacion}": map[string]any{}, "/afiliados/verificaciones": map[string]any{}}}
	b, _ := json.Marshal(doc)
	p := filepath.Join(t.TempDir(), "openapi.json")
	_ = os.WriteFile(p, b, 0o644)
	s := spec(up.URL)
	s.OpenAPI = p
	_, logs := through(t, s, "GET", "/certificados/afiliados/1000000001", nil, nil)
	if !strings.Contains(logs, `"route":"/certificados/afiliados/{numeroIdentificacion}"`) || strings.Contains(logs, "1000000001") {
		t.Fatalf("log: %s", logs)
	}
	_, logs = through(t, s, "POST", "/certificados/afiliados/verificaciones", strings.NewReader("{}"), nil)
	if !strings.Contains(logs, `"route":"/certificados/afiliados/verificaciones"`) {
		t.Fatalf("a literal path must win over a template: %s", logs)
	}
}

func TestProbe(t *testing.T) {
	up, _ := echoUpstream(t)
	if err := Probe(context.Background(), spec(up.URL)); err != nil {
		t.Fatal(err)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if err := Probe(context.Background(), spec("http://"+addr)); err == nil {
		t.Fatal("Probe must fail when nothing listens")
	}
}
