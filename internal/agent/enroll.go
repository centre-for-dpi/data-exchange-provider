// Package agent is the node's control plane against the portal (contract v1): enrollment,
// heartbeat with health, certificate renewal and the configuration the network expects.
package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
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
	return fmt.Errorf("el token de enrolamiento %s existe pero el usuario del contenedor (uid 65532) no puede leerlo: "+
		"ajusta el dueño (chown 65532) o los permisos (644 dentro de una carpeta 700)", path)
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
				return nil, fmt.Errorf("la respuesta de enrolamiento del portal no es usable (%w); el token ya se consumió: pide uno nuevo y avisa al operador de la red", err)
			}
			if err := e.Store.Save(st); err != nil {
				return nil, fmt.Errorf("enrolado como %s, pero no se pudo guardar el estado: %w", res.NodeName, err)
			}
			e.Log.Info("enrolled", "node", st.NodeName, "tunnel", st.WireGuard.Address, "gateway_client", st.GatewayClientName)
			e.Log.Info("enrollment_token_used", "detail", "el token ya se usó: puedes borrar el secreto enrollment_token")
			return st, nil
		case status == http.StatusUnauthorized:
			return nil, errors.New("el portal rechazó el token de enrolamiento: no es válido, ya se usó o venció; pide uno nuevo en el portal (Mi entidad → Nodos del kit)")
		case status == http.StatusConflict:
			return nil, errors.New("el portal ya tiene enrolada esta clave WireGuard: el estado del nodo se perdió; usa install.sh reenrolar con un token nuevo")
		case status >= 400 && status < 500:
			return nil, fmt.Errorf("el portal rechazó el enrolamiento: %w", err)
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
			e.Log.Info("waiting_for_token", "detail", "esperando el token de enrolamiento en "+e.TokenFile)
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
		return errors.New("faltan nodeId, nodeName, gatewayClientName o controlUrl")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(st.CACertificate)) {
		return errors.New("la raíz de la red no es un certificado PEM")
	}
	pair, err := tls.X509KeyPair([]byte(st.Certificate), []byte(st.KeyPEM))
	if err != nil {
		return fmt.Errorf("el certificado no corresponde a la clave del nodo: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: st.NodeName, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return fmt.Errorf("el certificado no es de la red para %s: %w", st.NodeName, err)
	}
	if _, err := netip.ParsePrefix(st.WireGuard.Address); err != nil {
		return fmt.Errorf("dirección del túnel %q inválida", st.WireGuard.Address)
	}
	if _, err := tunnel.ParseKey(st.WireGuard.HubPublicKey); err != nil {
		return errors.New("clave pública del hub inválida")
	}
	if st.WireGuard.HubEndpoint == "" || len(st.WireGuard.AllowedIPs) == 0 {
		return errors.New("falta el endpoint del hub o allowedIps")
	}
	for _, a := range st.WireGuard.AllowedIPs {
		if _, err := netip.ParsePrefix(a); err != nil {
			return fmt.Errorf("allowedIps %q inválida", a)
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
