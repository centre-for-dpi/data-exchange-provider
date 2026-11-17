// Package agent is the node's control plane against the portal (contract v1): enrollment,
// heartbeat with health, certificate renewal and the configuration the network expects.
package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
	"github.com/centre-for-dpi/data-exchange-provider/internal/tunnel"
)

// Enroller turns a single-use token into the node's persistent identity.
type Enroller struct {
	Store       state.Store
	HTTP        *http.Client
	Portal      string
	TokenFile   string
	KitVersion  string
	ServicePort int
	Log         *slog.Logger

	TokenPoll          time.Duration                // default 10s
	RetryMin, RetryMax time.Duration                // default 2s and 60s
	ReadFile           func(string) ([]byte, error) // default os.ReadFile (tests inject failures)
}

// errTokenUnreadable explains the usual cause: Compose secrets are bind mounts that keep the
// host file's owner and mode, and the container runs as uid 65532.
func errTokenUnreadable(path string) error {
	return i18n.Errorf("the enrollment token %s exists but the container's user (uid 65532) cannot read it: "+
		"fix its owner (chown 65532) or its permissions (644 inside a 700 folder)", path)
}

// LoadOrEnroll returns the saved state, or enrolls when there is none. A state that exists but
// cannot be read is an error: enrolling again needs a new token, so it is never done silently.
func (e *Enroller) LoadOrEnroll(ctx context.Context) (*state.State, error) {
	st, err := e.Store.Load()
	if err == nil {
		return st, nil
	}
	if !errors.Is(err, state.ErrNoState) {
		return nil, err
	}
	e.defaults()
	token, err := e.waitForToken(ctx)
	if err != nil {
		return nil, err
	}
	keyPEM, csrPEM, err := NewKeyAndCSR()
	if err != nil {
		return nil, err
	}
	wgPriv, wgPub := tunnel.GenerateKey()
	req := contract.EnrollRequest{Token: token, CSR: csrPEM, WireGuardPublicKey: wgPub.String(), KitVersion: e.KitVersion, ServicePort: e.ServicePort}
	wait := e.RetryMin
	for {
		res, err := contract.Enroll(ctx, e.HTTP, e.Portal, req)
		switch status := contract.StatusOf(err); {
		case err == nil:
			st := fromEnrollment(res, keyPEM, wgPriv)
			if err := validateEnrollment(st); err != nil {
				return nil, i18n.Errorf("the portal's enrollment answer is not usable (%w); the token is already spent: ask for a new one and tell the network's operator", err)
			}
			if err := e.Store.Save(st); err != nil {
				return nil, i18n.Errorf("enrolled as %s, but the state could not be saved: %w", res.NodeName, err)
			}
			e.Log.Info("enrolled", "node", st.NodeName, "tunnel", st.WireGuard.Address, "gateway_client", st.GatewayClientName)
			e.Log.Info("enrollment_token_used", "detail", "the token was used: you can delete the enrollment_token secret")
			return st, nil
		case status == http.StatusUnauthorized:
			return nil, i18n.New("the portal rejected the enrollment token: it is not valid, was already used or expired; ask for a new one in the portal (My entity → Kit nodes)")
		case status == http.StatusConflict:
			return nil, i18n.New("the portal already has this WireGuard key enrolled: the node's state was lost; use install.sh reenroll with a new token")
		case status >= 400 && status < 500:
			return nil, i18n.Errorf("the portal rejected the enrollment: %w", err)
		default: // 5xx or network: the portal may be starting or unreachable for a while
			e.Log.Warn("enroll_retry", "error", err.Error(), "retry_in", wait.String())
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			wait = min(wait*2, e.RetryMax)
		}
	}
}

func (e *Enroller) defaults() {
	if e.TokenPoll == 0 {
		e.TokenPoll = 10 * time.Second
	}
	if e.RetryMin == 0 {
		e.RetryMin = 2 * time.Second
	}
	if e.RetryMax == 0 {
		e.RetryMax = 60 * time.Second
	}
	if e.ReadFile == nil {
		e.ReadFile = os.ReadFile
	}
}

// waitForToken reads the token secret, waiting (not crashing) until it is there.
func (e *Enroller) waitForToken(ctx context.Context) (string, error) {
	logged := false
	for {
		b, err := e.ReadFile(e.TokenFile)
		if errors.Is(err, fs.ErrPermission) {
			return "", errTokenUnreadable(e.TokenFile)
		}
		if err == nil {
			if token := strings.TrimSpace(string(b)); token != "" {
				return token, nil
			}
		}
		if !logged {
			e.Log.Info("waiting_for_token", "detail", "waiting for the enrollment token at "+e.TokenFile)
			logged = true
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(e.TokenPoll):
		}
	}
}

// validateEnrollment checks the answer before it becomes the node's identity: the names the TLS
// check relies on, the network root, a certificate that matches our key and names this node, and
// a tunnel configuration the device can take.
func validateEnrollment(st *state.State) error {
	if st.NodeID == "" || st.NodeName == "" || st.GatewayClientName == "" || st.ControlURL == "" {
		return i18n.New("nodeId, nodeName, gatewayClientName or controlUrl is missing")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(st.CACertificate)) {
		return i18n.New("the network's root is not a PEM certificate")
	}
	pair, err := tls.X509KeyPair([]byte(st.Certificate), []byte(st.KeyPEM))
	if err != nil {
		return i18n.Errorf("the certificate does not match the node's key: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	// The portal sends the chain (leaf + step-ca's intermediate) and only the root as the CA.
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		if c, err := x509.ParseCertificate(der); err == nil {
			intermediates.AddCert(c)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: st.NodeName,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return i18n.Errorf("the certificate is not the network's for %s: %w", st.NodeName, err)
	}
	if _, err := netip.ParsePrefix(st.WireGuard.Address); err != nil {
		return i18n.Errorf("invalid tunnel address %q", st.WireGuard.Address)
	}
	if _, err := tunnel.ParseKey(st.WireGuard.HubPublicKey); err != nil {
		return i18n.New("invalid hub public key")
	}
	if st.WireGuard.HubEndpoint == "" || len(st.WireGuard.AllowedIPs) == 0 {
		return i18n.New("the hub's endpoint or allowedIps is missing")
	}
	for _, a := range st.WireGuard.AllowedIPs {
		if _, err := netip.ParsePrefix(a); err != nil {
			return i18n.Errorf("invalid allowedIps %q", a)
		}
	}
	return nil
}

func fromEnrollment(r *contract.EnrollResponse, keyPEM string, wgPriv tunnel.Key) *state.State {
	return &state.State{
		NodeID: r.NodeID, NodeName: r.NodeName, Certificate: r.Certificate, CertificateNotAfter: r.CertificateNotAfter,
		CACertificate: r.CACertificate, ControlURL: r.ControlURL, GatewayClientName: r.GatewayClientName,
		HeartbeatIntervalSeconds: r.HeartbeatIntervalSeconds,
		WireGuard: state.WireGuard{
			Address: r.WireGuard.Address, HubPublicKey: r.WireGuard.HubPublicKey, HubEndpoint: r.WireGuard.HubEndpoint,
			HubAddress: r.WireGuard.HubAddress, AllowedIPs: r.WireGuard.AllowedIPs, PersistentKeepaliveSeconds: r.WireGuard.PersistentKeepaliveSeconds,
		},
		KeyPEM: keyPEM, WGPrivateKey: wgPriv.String(),
	}
}
