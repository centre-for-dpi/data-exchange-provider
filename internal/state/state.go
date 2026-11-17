// Package state keeps what the node must not lose across restarts: its identity, keys,
// certificate and tunnel configuration. Losing it means enrolling again with a new token.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// State is the node's persistent identity (from enrollment) and its local secrets.
type State struct {
	NodeID                   string    `json:"nodeId"`
	NodeName                 string    `json:"nodeName"`
	Certificate              string    `json:"certificate"`
	CertificateNotAfter      string    `json:"certificateNotAfter"`
	CACertificate            string    `json:"caCertificate"`
	ControlURL               string    `json:"controlUrl"`
	GatewayClientName        string    `json:"gatewayClientName"`
	HeartbeatIntervalSeconds int       `json:"heartbeatIntervalSeconds"`
	WireGuard                WireGuard `json:"wireguard"`

	// Local secrets: never logged, never sent.
	KeyPEM       string `json:"keyPem"`
	WGPrivateKey string `json:"wgPrivateKey"`

	// PendingRenewal is saved before calling /renew so that a lost response can be retried
	// with the same CSR within the contract's grace period.
	PendingRenewal *PendingRenewal `json:"pendingRenewal,omitempty"`
}

// WireGuard is the tunnel configuration handed out at enrollment.
type WireGuard struct {
	Address                    string   `json:"address"`
	HubPublicKey               string   `json:"hubPublicKey"`
	HubEndpoint                string   `json:"hubEndpoint"`
	HubAddress                 string   `json:"hubAddress"`
	AllowedIPs                 []string `json:"allowedIps"`
	PersistentKeepaliveSeconds int      `json:"persistentKeepaliveSeconds"`
}

// PendingRenewal is a renewal whose response has not been applied yet.
type PendingRenewal struct {
	KeyPEM    string    `json:"keyPem"`
	CSRPEM    string    `json:"csrPem"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrNoState means the node has never enrolled (no state file).
var ErrNoState = errors.New("no state")

const (
	stateFile  = "state.json"
	statusFile = "status.json"
)

// Store reads and writes the state directory (DEN_STATE_DIR).
type Store struct{ Dir string }

// Load returns ErrNoState when there is no state. Any other failure is an error the node must
// not paper over by enrolling again.
func (s Store) Load() (*State, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, i18n.Errorf("cannot read the node's state: %w; if it was lost, use install.sh reenroll", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, i18n.Errorf("the node's state (%s) is damaged: %w; restore a backup or use install.sh reenroll", filepath.Join(s.Dir, stateFile), err)
	}
	return &st, nil
}

// Save writes the state atomically with mode 0600.
func (s Store) Save(st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, stateFile), b, 0o600)
}

// WriteStatus writes status.json (no secrets) for `den-node status`.
func (s Store) WriteStatus(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, statusFile), b, 0o640)
}

// ReadStatus reads status.json.
func (s Store) ReadStatus(v any) error {
	b, err := os.ReadFile(filepath.Join(s.Dir, statusFile))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeAtomic writes a temporary file in the same directory, syncs it and renames it over the
// target, so a crash leaves either the old or the new content, never a partial file.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(mode); err != nil && !isWindows() {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync() // best effort: persist the rename
		d.Close()
	}
	return nil
}
