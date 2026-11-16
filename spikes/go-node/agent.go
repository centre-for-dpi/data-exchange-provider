// THROWAWAY SPIKE. The control side of the node against the real portal (contract v1): enroll
// with the single-use token, bring up the tunnel the portal hands out, serve on the tunnel
// address, and send heartbeats over the control channel with the node certificate.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const kitVersion = "0.0.1-spike"

type nodeState struct {
	NodeID                   string `json:"nodeId"`
	NodeName                 string `json:"nodeName"`
	Certificate              string `json:"certificate"`
	CertificateNotAfter      string `json:"certificateNotAfter"`
	CACertificate            string `json:"caCertificate"`
	ControlURL               string `json:"controlUrl"`
	GatewayClientName        string `json:"gatewayClientName"`
	HeartbeatIntervalSeconds int    `json:"heartbeatIntervalSeconds"`
	Wireguard                struct {
		Address                    string   `json:"address"`
		HubPublicKey               string   `json:"hubPublicKey"`
		HubEndpoint                string   `json:"hubEndpoint"`
		HubAddress                 string   `json:"hubAddress"`
		AllowedIps                 []string `json:"allowedIps"`
		PersistentKeepaliveSeconds int      `json:"persistentKeepaliveSeconds"`
	} `json:"wireguard"`
	// Local secrets: never sent anywhere.
	KeyPEM    string `json:"keyPem"`
	WGPrivate string `json:"wgPrivate"` // base64
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s node: %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func enroll(portal, token string, servicePort int) (*nodeState, error) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "rid-node"}}, key)
	if err != nil {
		return nil, err
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	wg := newWGKey()
	body, _ := json.Marshal(map[string]any{
		"token":              token,
		"csr":                string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		"wireguardPublicKey": base64.StdEncoding.EncodeToString(wg.public[:]),
		"kitVersion":         kitVersion,
		"servicePort":        servicePort,
	})
	resp, err := http.Post(strings.TrimRight(portal, "/")+"/api/v1/nodes/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("enrollment refused: %d %s", resp.StatusCode, raw)
	}
	var st nodeState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	st.KeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	st.WGPrivate = base64.StdEncoding.EncodeToString(wg.private[:])
	return &st, nil
}

func b64Key(s string) (k [32]byte, err error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return k, fmt.Errorf("bad WireGuard key")
	}
	copy(k[:], raw)
	return k, nil
}

// runNode is the container entrypoint: state survives restarts in RID_STATE_DIR.
func runNode(ctx context.Context) {
	stateDir := env("RID_STATE_DIR", "/var/lib/rid-node")
	stateFile := filepath.Join(stateDir, "state.json")
	servicePort := 8443
	servicePath := env("RID_SERVICE_PATH", "/salud-afiliacion")

	// The instant API first: if the declaration does not match the catalog, do not enroll.
	d := must(dialectFor(env("RID_DB_ENGINE", "postgres")))
	db := must(sql.Open(d.driver(), os.Getenv("RID_DB_DSN")))
	db.SetMaxOpenConns(4)
	svc := must(loadService(env("RID_SERVICE_FILE", "service.yaml")))
	var res []compiledResource
	for attempt := 0; ; attempt++ { // the database may still be starting
		cat, err := readCatalog(ctx, db, d)
		if err == nil {
			res, err = compile(svc, cat, d)
		}
		if err == nil {
			break
		}
		if attempt > 60 {
			must(0, err)
		}
		logf("waiting for the database: %v", err)
		time.Sleep(5 * time.Second)
	}
	openapi := must(buildOpenAPI(svc, res))
	sum := sha256.Sum256(openapi)
	openapiSHA := hex.EncodeToString(sum[:])
	logf("instant API %s ready: %d resources, OpenAPI sha256 %s", svc.Service, len(res), openapiSHA)

	var st *nodeState
	if raw, err := os.ReadFile(stateFile); err == nil {
		st = new(nodeState)
		must(0, json.Unmarshal(raw, st))
		logf("state found: %s", st.NodeName)
	} else {
		token := os.Getenv("RID_ENROLLMENT_TOKEN")
		for token == "" {
			if raw, err := os.ReadFile(filepath.Join(stateDir, "token")); err == nil {
				token = strings.TrimSpace(string(raw))
				break
			}
			time.Sleep(2 * time.Second)
		}
		st = must(enroll(env("RID_PORTAL_URL", "http://host.docker.internal:4000"), token, servicePort))
		raw, _ := json.Marshal(st)
		must(0, os.WriteFile(stateFile, raw, 0o600))
		logf("enrolled as %s, tunnel %s, gateway client %s", st.NodeName, st.Wireguard.Address, st.GatewayClientName)
	}

	// Tunnel: user space, the portal's configuration. The hub endpoint is a name: resolve it.
	wgPriv := must(b64Key(st.WGPrivate))
	hubPub := must(b64Key(st.Wireguard.HubPublicKey))
	hubUDP := must(net.ResolveUDPAddr("udp", st.Wireguard.HubEndpoint))
	addr := netip.MustParsePrefix(st.Wireguard.Address).Addr()
	var k wgKey
	k.private = wgPriv
	_, tnet := must2(startPeer(peerConfig{address: addr, key: k, peerPublic: hubPub,
		allowedIP: strings.Join(st.Wireguard.AllowedIps, "\nallowed_ip="), endpoint: hubUDP.String()}))
	logf("tunnel up: %s -> hub %s (%s)", addr, st.Wireguard.HubEndpoint, hubUDP)

	cert := must(tls.X509KeyPair([]byte(st.Certificate), []byte(st.KeyPEM)))
	var live atomic.Pointer[tls.Certificate] // swapped on renewal, for serving and for control
	live.Store(&cert)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(st.CACertificate))
	l := must(tnet.ListenTCP(&net.TCPAddr{IP: net.IP(addr.AsSlice()), Port: servicePort}))
	serveNode(l, nodeConfig{nodeName: st.NodeName, gatewayClientName: st.GatewayClientName, caPool: pool,
		current: live.Load}, []service{{servicePath, instantHandler(db, res, servicePath)}})
	logf("serving %s on https://%s:%d (only %s)", servicePath, addr, servicePort, st.GatewayClientName)

	// Control channel: TLS to the gateway with the node certificate. In development the
	// controlUrl's host is localhost: dial RID_CONTROL_HOST but keep the TLS name.
	control := must(url.Parse(st.ControlURL))
	port := control.Port()
	if port == "" {
		port = "443"
	}
	dialHost := env("RID_CONTROL_HOST", control.Hostname())
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: control.Hostname(), MinVersion: tls.VersionTLS12,
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return live.Load(), nil }},
		DisableKeepAlives: true, // a renewed certificate must be used on the next request
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(dialHost, port))
		},
	}}
	base := strings.TrimRight(st.ControlURL, "/") + "/api/v1/nodes/" + st.NodeID
	every := time.Duration(max(st.HeartbeatIntervalSeconds, 5)) * time.Second
	if s := os.Getenv("RID_HEARTBEAT_SECONDS"); s != "" {
		var n int
		fmt.Sscan(s, &n)
		every = time.Duration(n) * time.Second
	}
	renewOnce := os.Getenv("RID_RENEW_ONCE") == "1"
	renew := func() {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		csrDER, _ := x509.CreateCertificateRequest(rand.Reader,
			&x509.CertificateRequest{Subject: pkix.Name{CommonName: "rid-node"}}, key)
		body, _ := json.Marshal(map[string]string{
			"csr": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))})
		resp, err := client.Post(base+"/renew", "application/json", bytes.NewReader(body))
		if err != nil {
			logf("renew failed: %v", err)
			return
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var out struct {
			Certificate         string `json:"certificate"`
			CertificateNotAfter string `json:"certificateNotAfter"`
		}
		if resp.StatusCode != 200 || json.Unmarshal(raw, &out) != nil {
			logf("renew refused: %d %s", resp.StatusCode, raw)
			return
		}
		keyDER, _ := x509.MarshalECPrivateKey(key)
		keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
		next, err := tls.X509KeyPair([]byte(out.Certificate), []byte(keyPEM))
		if err != nil {
			logf("renewed certificate unusable: %v", err)
			return
		}
		old, _ := x509.ParseCertificate(live.Load().Certificate[0])
		fresh, _ := x509.ParseCertificate(next.Certificate[0])
		// Persist first: if the process dies now, the next start uses the new certificate.
		st.Certificate, st.CertificateNotAfter, st.KeyPEM = out.Certificate, out.CertificateNotAfter, keyPEM
		raw, _ = json.Marshal(st)
		must(0, os.WriteFile(stateFile, raw, 0o600))
		live.Store(&next)
		logf("certificate renewed: serial %s -> %s, notAfter %s", old.SerialNumber, fresh.SerialNumber, out.CertificateNotAfter)
	}
	for {
		body, _ := json.Marshal(map[string]any{
			"kitVersion": kitVersion, "servicePort": servicePort,
			"services": []map[string]string{{"name": svc.Service, "path": servicePath, "openapiSha256": openapiSHA}},
			"health":   map[string]string{"status": "ok"},
		})
		if resp, err := client.Post(base+"/heartbeat", "application/json", bytes.NewReader(body)); err != nil {
			logf("heartbeat failed: %v", err)
		} else {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logf("heartbeat %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			var hb struct {
				RenewCertificate bool `json:"renewCertificate"`
			}
			_ = json.Unmarshal(raw, &hb)
			if resp.StatusCode == 200 && (hb.RenewCertificate || renewOnce) {
				renewOnce = false
				renew()
			}
		}
		if resp, err := client.Get(base + "/config"); err == nil {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logf("config %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		} else if !errors.Is(err, context.Canceled) {
			logf("config failed: %v", err)
		}
		time.Sleep(every)
	}
}
