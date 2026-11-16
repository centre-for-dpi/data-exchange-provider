// THROWAWAY SPIKE (ADR-0001). One Go binary as the provider node:
//
//	gonode catalog            what the read-only user can see
//	gonode openapi <out.json> OpenAPI generated from service.yaml (validated against the catalog)
//	gonode demo               hub + node over user-space WireGuard on localhost, then calls as
//	                          the gateway (and as impostors) through the tunnel
//
//	gonode check              the same requests against the instant API, no tunnel (per engine)
//	gonode run                the real node in a container: enroll, tunnel, serve, heartbeat
//
// The database: RID_DB_ENGINE (sqlserver | postgres | mysql | oracle) and RID_DB_DSN.
package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"time"
)

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return v
}

func openDB() (*sql.DB, dialect) {
	d := must(dialectFor(env("RID_DB_ENGINE", "sqlserver")))
	db := must(sql.Open(d.driver(), env("RID_DB_DSN", os.Getenv("RID_SPIKE_DB"))))
	db.SetMaxOpenConns(4)
	return db, d
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: gonode catalog | openapi <out.json> | check | demo | run")
		os.Exit(2)
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "catalog":
		db, d := openDB()
		for _, o := range must(readCatalog(ctx, db, d)) {
			fmt.Printf("%s %s.%s\n", o.Kind, o.Schema, o.Name)
			for _, c := range o.Columns {
				_, ok := jsonType(c)
				fmt.Printf("    %-18s %-28s kind=%-11s exposable=%v\n", c.Name, c.Type, c.Kind, ok)
			}
		}
	case "openapi":
		db, d := openDB()
		svc := must(loadService("service.yaml"))
		res := must(compile(svc, must(readCatalog(ctx, db, d)), d))
		must(0, os.WriteFile(os.Args[2], must(buildOpenAPI(svc, res)), 0o644))
		fmt.Println("written", os.Args[2])
	case "check":
		check(ctx)
	case "demo":
		demo(ctx)
	case "run":
		runNode(ctx)
	}
}

// check runs the instant API in-process (no tunnel, no TLS) with the same requests on any engine.
func check(ctx context.Context) {
	db, d := openDB()
	svc := must(loadService("service.yaml"))
	res := must(compile(svc, must(readCatalog(ctx, db, d)), d))
	h := accessLog(instantHandler(db, res, ""))
	for _, path := range []string{
		"/afiliados/1000000001", "/afiliados/9999999999", "/afiliados/abc'--",
		"/afiliados/1000000001?x=1", "/afiliados", "/hogares/1",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		fmt.Printf("%-28s -> %d %s\n", path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func demo(ctx context.Context) {
	const (
		nodeName          = "rid-node-0193a1b2-0000-7000-8000-000000000001"
		gatewayClientName = "rid-gateway.salud"
		servicePort       = 8443
	)
	hubAddr, nodeAddr := netip.MustParseAddr("10.77.0.1"), netip.MustParseAddr("10.77.0.2")
	pki := newTestPKI(nodeName, gatewayClientName)

	// 1. Hub and node, both in user space. The node only dials out (random UDP port).
	hubKey, nodeKey := newWGKey(), newWGKey()
	_, hubNet := must2(startPeer(peerConfig{address: hubAddr, listenPort: 51999, key: hubKey,
		peerPublic: nodeKey.public, allowedIP: nodeAddr.String() + "/32"}))
	_, nodeNet := must2(startPeer(peerConfig{address: nodeAddr, key: nodeKey, peerPublic: hubKey.public,
		allowedIP: hubAddr.String() + "/32", endpoint: "127.0.0.1:51999"}))

	// 2. The entity's own API (stands in for an existing backend) on loopback.
	own := must(net.Listen("tcp", "127.0.0.1:0"))
	go http.Serve(own, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"api":"propia","path":%q,"credential_received":%v,"consumer":%q}`,
			r.URL.Path, r.Header.Get("X-Api-Key") != "", r.Header.Get("X-RID-Consumer-Entity"))
	}))

	// 3. The node: instant API + own API behind one TLS listener on the TUNNEL address only.
	db, d := openDB()
	svc := must(loadService("service.yaml"))
	res := must(compile(svc, must(readCatalog(ctx, db, d)), d))
	l := must(nodeNet.ListenTCP(&net.TCPAddr{IP: net.IP(nodeAddr.AsSlice()), Port: servicePort}))
	serveNode(l, nodeConfig{nodeName: nodeName, gatewayClientName: gatewayClientName, cert: pki.node, caPool: pki.caPool}, []service{
		{"/salud-afiliacion", instantHandler(db, res, "/salud-afiliacion")},
		ownAPIProxy("/api-propia", "http://"+own.Addr().String(), "X-Api-Key", "local-secret-never-leaves"),
	})

	// 4. Calls as the hub/gateway would make them: TCP through the tunnel, SNI and Host = node name.
	client := func(cert *tls.Certificate) *http.Client {
		cfg := &tls.Config{RootCAs: pki.caPool, ServerName: nodeName, MinVersion: tls.VersionTLS13}
		if cert != nil {
			// Always present it, even when the node only advertises the network CA: the node itself
			// must be the one that rejects an impostor.
			cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return cert, nil }
		}
		return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			TLSClientConfig: cfg,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return hubNet.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(nodeAddr, servicePort))
			},
		}}
	}
	call := func(label string, c *http.Client, path string) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("https://%s:%d%s", nodeName, servicePort, path), nil)
		req.Header.Set("X-Request-Id", "req-"+strings.ReplaceAll(label, " ", "-"))
		req.Header.Set("X-RID-Consumer-Entity", "entidad-consumidora")
		resp, err := c.Do(req)
		if err != nil {
			fmt.Printf("%-34s -> REJECTED: %v\n", label, err)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("%-34s -> %d %s\n", label, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	gw := client(&pki.gateway)
	fmt.Println("== as the gateway ==")
	call("by key", gw, "/salud-afiliacion/afiliados/1000000001")
	call("by key, not found", gw, "/salud-afiliacion/afiliados/9999999999")
	call("by key, bad format", gw, "/salud-afiliacion/afiliados/abc'--")
	call("query string", gw, "/salud-afiliacion/afiliados/1000000001?$filter=x")
	call("listing (undeclared)", gw, "/salud-afiliacion/afiliados")
	call("many rows (hogar)", gw, "/salud-afiliacion/hogares/1")
	call("own API", gw, "/api-propia/v1/certificados/abc")
	fmt.Println("== impostors ==")
	call("consumer app cert (same CA)", client(&pki.consumerApp), "/salud-afiliacion/afiliados/1000000001")
	call("gateway name, foreign CA", client(&pki.foreignGateway), "/salud-afiliacion/afiliados/1000000001")
	call("no client certificate", client(nil), "/salud-afiliacion/afiliados/1000000001")
	fmt.Println("== from the host, outside the tunnel ==")
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", servicePort), 2*time.Second); err != nil {
		fmt.Printf("%-34s -> %v\n", "tcp 127.0.0.1:8443", err)
	} else {
		fmt.Println("tcp 127.0.0.1:8443 -> CONNECTED (unexpected)")
	}
	time.Sleep(300 * time.Millisecond) // let the access log flush
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return a, b
}
