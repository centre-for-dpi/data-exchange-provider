// Package config reads the node's configuration (node.yaml): the portal, and the services the
// node serves behind servicePath prefixes. Secrets never live here, only paths to secret files.
package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// Node is node.yaml.
type Node struct {
	Portal string `yaml:"portal"` // portal API, used to enroll
	// ControlDialAddress (development only) is the host:port dialed for the control channel
	// instead of the controlUrl's host; the TLS name stays the controlUrl's.
	ControlDialAddress string           `yaml:"controlDialAddress"`
	GatewayAssertion   GatewayAssertion `yaml:"gatewayAssertion"`
	Services           []Service        `yaml:"services"`
}

// GatewayAssertion is how the node treats the gateway's signed assertion (X-DEN-Assertion, the
// portal's ADR-0024).
type GatewayAssertion struct {
	// Mode: verify (default: verify and log the result, never reject; the network may have the
	// assertion off), require (401 without a valid one) or off.
	Mode string `yaml:"mode"`
	// Audience is the provider entity's slug; empty: taken from the gateway's client name
	// (den-gateway.<slug>) given at enrollment.
	Audience string `yaml:"audience"`
	// Issuer is the gateway's public URL (the assertion's iss); empty: not checked.
	Issuer string `yaml:"issuer"`
}

// Service is one servicePath prefix the node serves.
type Service struct {
	Name    string      `yaml:"name"` // name in the heartbeat
	Path    string      `yaml:"path"` // servicePath registered in the portal
	Type    string      `yaml:"type"` // "proxy" | "instant"
	Proxy   ProxySpec   `yaml:",inline"`
	Instant InstantSpec `yaml:",inline"`
	File    string      `yaml:"-"` // services/<name>.yaml it came from, if any (for error messages)
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
		return nil, i18n.Errorf("cannot read the configuration %s: %w", path, err)
	}
	var n Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&n); err != nil {
		return nil, i18n.Errorf("invalid configuration %s: %w", path, err)
	}
	more, err := loadServiceFiles(filepath.Join(filepath.Dir(path), "services"))
	if err != nil {
		return nil, err
	}
	n.Services = append(n.Services, more...)
	if n.GatewayAssertion.Mode == "" {
		n.GatewayAssertion.Mode = "verify"
	}
	for i := range n.Services {
		n.Services[i].applyDefaults()
	}
	if err := n.Validate(); err != nil {
		return nil, i18n.Errorf("invalid configuration %s:\n%w", path, err)
	}
	return &n, nil
}

// loadServiceFiles reads services/*.yaml, one service per file, in name order. Files with
// ".openapi." in their name are OpenAPI documents next to the services, not services.
func loadServiceFiles(dir string) ([]Service, error) {
	yamlFiles, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	ymlFiles, _ := filepath.Glob(filepath.Join(dir, "*.yml"))
	files := append(yamlFiles, ymlFiles...)
	sort.Strings(files)
	var out []Service
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, ".") || strings.Contains(base, ".openapi.") {
			continue
		}
		s, err := readServiceFile(f)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func readServiceFile(f string) (Service, error) {
	raw, err := os.ReadFile(f)
	if err != nil {
		return Service{}, i18n.Errorf("cannot read the service %s: %w", f, err)
	}
	return decodeService(raw, f)
}

func decodeService(raw []byte, f string) (Service, error) {
	var s Service
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Service{}, i18n.Errorf("invalid service %s: %w", f, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Service{}, i18n.Errorf("invalid service %s: each file must hold a single service (no --- separators)", f)
	}
	s.File = f
	return s, nil
}

// LoadServiceFile reads one services/<name>.yaml on its own (for the commands that work on a
// single service), with defaults, and validates it.
func LoadServiceFile(path string) (Service, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Service{}, i18n.Errorf("cannot read the service %s: %w", path, err)
	}
	return ParseService(raw, path)
}

// ParseService is LoadServiceFile on content already in memory; path names it in errors.
func ParseService(raw []byte, path string) (Service, error) {
	s, err := decodeService(raw, path)
	if err != nil {
		return Service{}, err
	}
	s.applyDefaults()
	if errs := validateServices([]Service{s}); len(errs) > 0 {
		return Service{}, i18n.Errorf("invalid service %s:\n%w", path, errors.Join(errs...))
	}
	return s, nil
}

func (s *Service) applyDefaults() {
	switch s.Type {
	case "proxy":
		s.Proxy.applyDefaults()
	case "instant":
		s.Instant.applyDefaults(s.Name)
	}
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
