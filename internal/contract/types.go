// Package contract is the kit's side of the portal's node contract v1
// (../data-exchange/docs/contracts/node-contract-v1.openapi.json): enroll, heartbeat, renew and
// config. The tests validate these types against a copy of that document.
package contract

// EnrollRequest is POST /api/v1/nodes/enroll.
type EnrollRequest struct {
	Token              string `json:"token"`
	CSR                string `json:"csr"`
	WireGuardPublicKey string `json:"wireguardPublicKey"`
	KitVersion         string `json:"kitVersion"`
	ServicePort        int    `json:"servicePort"`
}

// EnrollResponse is the 201 of enroll.
type EnrollResponse struct {
	ContractVersion          string    `json:"contractVersion"`
	NodeID                   string    `json:"nodeId"`
	NodeName                 string    `json:"nodeName"`
	Certificate              string    `json:"certificate"`
	CertificateNotAfter      string    `json:"certificateNotAfter"`
	CACertificate            string    `json:"caCertificate"`
	ControlURL               string    `json:"controlUrl"`
	GatewayClientName        string    `json:"gatewayClientName"`
	HeartbeatIntervalSeconds int       `json:"heartbeatIntervalSeconds"`
	WireGuard                WireGuard `json:"wireguard"`
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

// HeartbeatRequest is POST /api/v1/nodes/{nodeId}/heartbeat.
type HeartbeatRequest struct {
	KitVersion  string        `json:"kitVersion"`
	ServicePort int           `json:"servicePort"`
	Services    []ServiceInfo `json:"services"`
	Health      Health        `json:"health"`
}

// ServiceInfo is one service the node declares.
type ServiceInfo struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	OpenAPISHA256 string `json:"openapiSha256,omitempty"`
}

// Health of the node: ok | degraded | failing.
type Health struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// HeartbeatResponse is the 200 of heartbeat.
type HeartbeatResponse struct {
	Status               string `json:"status"`
	NextHeartbeatSeconds int    `json:"nextHeartbeatSeconds"`
	RenewCertificate     bool   `json:"renewCertificate"`
}

// RenewRequest is POST /api/v1/nodes/{nodeId}/renew.
type RenewRequest struct {
	CSR string `json:"csr"`
}

// RenewResponse is the 200 of renew.
type RenewResponse struct {
	Certificate         string `json:"certificate"`
	CertificateNotAfter string `json:"certificateNotAfter"`
	PreviousValidUntil  string `json:"previousValidUntil"`
}

// ConfigResponse is GET /api/v1/nodes/{nodeId}/config.
type ConfigResponse struct {
	ContractVersion          string             `json:"contractVersion"`
	NodeID                   string             `json:"nodeId"`
	HeartbeatIntervalSeconds int                `json:"heartbeatIntervalSeconds"`
	MinimumKitVersion        *string            `json:"minimumKitVersion"`
	Services                 []PublishedService `json:"services"`
	Notices                  []Notice           `json:"notices"`
}

// PublishedService is a published version served by this node.
type PublishedService struct {
	APIVersionID string `json:"apiVersionId"`
	APIName      string `json:"apiName"`
	Version      string `json:"version"`
	BasePath     string `json:"basePath"`
	ServicePath  string `json:"servicePath"`
}

// Notice is a message from the portal to the node's operators.
type Notice struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
