package agent

import (
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
)

// Status is status.json: what `den-node status` shows. No secrets.
type Status struct {
	NodeID              string          `json:"nodeId"`
	NodeName            string          `json:"nodeName"`
	PortalState         string          `json:"portalState"` // portal's status, or unreachable / rejected
	LastHeartbeat       time.Time       `json:"lastHeartbeat"`
	LastHandshake       time.Time       `json:"lastHandshake"`
	CertificateNotAfter string          `json:"certificateNotAfter"`
	Health              contract.Health `json:"health"`
	Services            []ServiceStatus `json:"services"`
	Notices             []string        `json:"notices"`
	KitVersion          string          `json:"kitVersion"`
	Variant             string          `json:"variant"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}

// ServiceStatus is one service's health.
type ServiceStatus struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
	// Since the node started; latency over the recent requests, in milliseconds.
	Requests     int64   `json:"requests"`
	ClientErrors int64   `json:"clientErrors"`
	ServerErrors int64   `json:"serverErrors"`
	P50Ms        float64 `json:"p50Ms"`
	P95Ms        float64 `json:"p95Ms"`
}
