// Package config reads the node's configuration (node.yaml): the portal, and the services the
// node serves behind servicePath prefixes. Secrets never live here, only paths to secret files.
package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Node is node.yaml.
type Node struct {
	Portal string `yaml:"portal"` // portal API, used to enroll
	// ControlDialAddress (development only) is the host:port dialed for the control channel
	// instead of the controlUrl's host; the TLS name stays the controlUrl's.
	ControlDialAddress string    `yaml:"controlDialAddress"`
	Services           []Service `yaml:"services"`
}

// Service is one servicePath prefix the node serves.
type Service struct {
	Name  string    `yaml:"name"` // name in the heartbeat
	Path  string    `yaml:"path"` // servicePath registered in the portal
	Type  string    `yaml:"type"` // "proxy" | "instant"
	Proxy ProxySpec `yaml:",inline"`
}

// ProxySpec is a proxy service: the entity's own API behind the node.
type ProxySpec struct {
	Upstream        string        `yaml:"upstream"`
	AllowPlainHTTP  bool          `yaml:"allowPlainHttp"`
	CAFile          string        `yaml:"caFile"`
	Credential      *Credential   `yaml:"credential"`
	MaxRequestBytes int64         `yaml:"maxRequestBytes"`
	ConnectTimeout  time.Duration `yaml:"connectTimeout"`
	ResponseTimeout time.Duration `yaml:"responseTimeout"` // until the response headers
	OpenAPI         string        `yaml:"openapi"`         // optional: route templates for the logs
}

// Credential toward the upstream: exactly one of a static header, Basic, or a client certificate.
type Credential struct {
	Header     string `yaml:"header"`
	File       string `yaml:"file"`
	Basic      *Basic `yaml:"basic"`
	ClientCert string `yaml:"clientCert"`
	ClientKey  string `yaml:"clientKey"`
}

// Basic authentication toward the upstream.
type Basic struct {
	User         string `yaml:"user"`
	PasswordFile string `yaml:"passwordFile"`
}

// Defaults for proxy services.
const (
	DefaultMaxRequestBytes = 1 << 20
	DefaultConnectTimeout  = 5 * time.Second
	DefaultResponseTimeout = 30 * time.Second
)

// Load reads node.yaml strictly (unknown fields are errors), applies defaults and validates.
func Load(path string) (*Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la configuración %s: %w", path, err)
	}
	var n Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("configuración %s inválida: %w", path, err)
	}
	for i := range n.Services {
		if n.Services[i].Type == "proxy" {
			n.Services[i].Proxy.applyDefaults()
		}
	}
	if err := n.Validate(); err != nil {
		return nil, fmt.Errorf("configuración %s inválida:\n%w", path, err)
	}
	return &n, nil
}

func (p *ProxySpec) applyDefaults() {
	if p.MaxRequestBytes == 0 {
		p.MaxRequestBytes = DefaultMaxRequestBytes
	}
	if p.ConnectTimeout == 0 {
		p.ConnectTimeout = DefaultConnectTimeout
	}
	if p.ResponseTimeout == 0 {
		p.ResponseTimeout = DefaultResponseTimeout
	}
}
