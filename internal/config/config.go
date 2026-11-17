// Package config reads the node's configuration (node.yaml): the portal, and the services the
// node serves behind servicePath prefixes. Secrets never live here, only paths to secret files.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		return nil, fmt.Errorf("no se pudo leer la configuración %s: %w", path, err)
	}
	var n Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&n); err != nil {
		return nil, fmt.Errorf("configuración %s inválida: %w", path, err)
	}
	more, err := loadServiceFiles(filepath.Join(filepath.Dir(path), "services"))
	if err != nil {
		return nil, err
	}
	n.Services = append(n.Services, more...)
	for i := range n.Services {
		n.Services[i].applyDefaults()
	}
	if err := n.Validate(); err != nil {
		return nil, fmt.Errorf("configuración %s inválida:\n%w", path, err)
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
		return Service{}, fmt.Errorf("no se pudo leer el servicio %s: %w", f, err)
	}
	var s Service
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Service{}, fmt.Errorf("servicio %s inválido: %w", f, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Service{}, fmt.Errorf("servicio %s inválido: cada archivo debe tener un solo servicio (sin separadores ---)", f)
	}
	s.File = f
	return s, nil
}

// LoadServiceFile reads one services/<name>.yaml on its own (for the commands that work on a
// single service), with defaults, and validates it.
func LoadServiceFile(path string) (Service, error) {
	s, err := readServiceFile(path)
	if err != nil {
		return Service{}, err
	}
	s.applyDefaults()
	if errs := validateServices([]Service{s}); len(errs) > 0 {
		return Service{}, fmt.Errorf("servicio %s inválido:\n%w", path, errors.Join(errs...))
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
