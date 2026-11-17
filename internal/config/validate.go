package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/buildinfo"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// Patterns of the node contract v1 (heartbeat services[].name and .path).
var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	pathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~\-/]*$`)
)

// Validate returns every problem at once, in the operator's language (DEN_LANG), naming the service and the field.
func (n *Node) Validate() error {
	var errs []error
	add := func(err error) { errs = append(errs, err) }

	if err := validatePortal(n.Portal); err != nil {
		errs = append(errs, err)
	}
	if n.ControlDialAddress != "" {
		if _, _, err := net.SplitHostPort(n.ControlDialAddress); err != nil {
			add(i18n.Errorf("controlDialAddress %q must be host:port", n.ControlDialAddress))
		}
	}
	switch n.GatewayAssertion.Mode {
	case "verify", "require", "off":
	default:
		add(i18n.Errorf("gatewayAssertion.mode %q does not exist (verify, require or off)", n.GatewayAssertion.Mode))
	}
	errs = append(errs, validateServices(n.Services)...)
	return errors.Join(errs...)
}

// validateServices checks the services: each one, and that names and paths do not collide.
func validateServices(services []Service) []error {
	var errs []error
	add := func(err error) { errs = append(errs, err) }
	names := map[string]bool{}
	paths := map[string]string{}
	for i, s := range services {
		label := i18n.Sprintf("service %d (%q)", i+1, s.Name)
		if s.File != "" {
			label = i18n.Sprintf("service %q (%s)", s.Name, filepath.Base(s.File))
		}
		if !namePattern.MatchString(s.Name) || len(s.Name) > 80 {
			add(i18n.Errorf("%s: the name %q must use only lower-case letters, digits and hyphens (at most 80)", label, s.Name))
		}
		if names[s.Name] {
			add(i18n.Errorf("%s: the name %q is repeated", label, s.Name))
		}
		names[s.Name] = true
		if !pathPattern.MatchString(s.Path) || len(s.Path) > 200 || strings.HasSuffix(s.Path, "/") || hasDotSegment(s.Path) {
			add(i18n.Errorf("%s: the path %q must start with / followed by a name and use only letters, digits and . _ ~ - / (at most 200, without a trailing / or . and .. segments)", label, s.Path))
		} else {
			for other, otherName := range paths {
				switch {
				case other == s.Path:
					add(i18n.Errorf("%s: the path %q is repeated (%q uses it too)", label, s.Path, otherName))
				case overlaps(other, s.Path):
					add(i18n.Errorf("%s: the path %q overlaps %q of service %q", label, s.Path, other, otherName))
				}
			}
			paths[s.Path] = s.Name
		}
		switch s.Type {
		case "proxy":
			if f := s.Instant.setFields(); len(f) > 0 {
				add(i18n.Errorf("%s: a proxy service does not take %s (they are fields of the instant APIs)", label, strings.Join(f, ", ")))
			}
			for _, err := range s.Proxy.validate() {
				add(fmt.Errorf("%s: %v", label, err))
			}
		case "instant":
			if !buildinfo.InstantSupported() {
				add(i18n.Errorf("%s: canal mode does not include the instant APIs; use the full image (install.sh mode)", label))
				break
			}
			if f := s.Proxy.setFields(); len(f) > 0 {
				add(i18n.Errorf("%s: an instant service does not take %s (they are fields of proxy services)", label, strings.Join(f, ", ")))
			}
			for _, err := range s.Instant.validate() {
				add(fmt.Errorf("%s: %v", label, err))
			}
		default:
			add(i18n.Errorf("%s: the type %q does not exist (proxy or instant)", label, s.Type))
		}
	}
	return errs
}

func hasDotSegment(p string) bool {
	for _, s := range strings.Split(p, "/")[1:] {
		if s == "" || s == "." || s == ".." {
			return true
		}
	}
	return false
}

// overlaps reports whether one path is a prefix of the other at a segment boundary.
func overlaps(a, b string) bool {
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

func validatePortal(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return i18n.Errorf("portal %q must be an https:// URL", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	dev := host == "localhost" || host == "host.docker.internal" || !strings.Contains(host, ".")
	if u.Scheme == "http" && dev {
		return nil
	}
	return i18n.Errorf("portal %q must use https:// (http only for local development)", raw)
}

func (p ProxySpec) validate() []error {
	var errs []error
	u, err := url.Parse(p.Upstream)
	switch {
	case err != nil || u.Host == "":
		errs = append(errs, i18n.Errorf("upstream %q must be a full http(s) URL", p.Upstream))
	case u.User != nil:
		errs = append(errs, i18n.Errorf("upstream must not carry a user or password in the URL: use credential.basic with passwordFile"))
	case u.RawQuery != "" || u.Fragment != "":
		errs = append(errs, i18n.Errorf("upstream %q must not carry a query or a fragment", p.Upstream))
	case u.Scheme == "http" && !p.AllowPlainHTTP:
		errs = append(errs, i18n.Errorf("upstream %q uses unencrypted http: add allowPlainHttp: true if it is an internal API on the LAN", p.Upstream))
	case u.Scheme != "http" && u.Scheme != "https":
		errs = append(errs, i18n.Errorf("upstream %q must use http or https", p.Upstream))
	}
	if p.MaxRequestBytes < 0 || p.ConnectTimeout < 0 || p.ResponseTimeout < 0 {
		errs = append(errs, i18n.New("maxRequestBytes, connectTimeout and responseTimeout cannot be negative"))
	}
	if c := p.Credential; c != nil {
		forms := 0
		if c.Header != "" || c.File != "" {
			forms++
			if c.Header == "" || c.File == "" {
				errs = append(errs, i18n.New("header credential: header and file are required"))
			}
		}
		if c.Basic != nil {
			forms++
			if c.Basic.User == "" || c.Basic.PasswordFile == "" {
				errs = append(errs, i18n.New("basic credential: user and passwordFile are required"))
			}
		}
		if c.ClientCert != "" || c.ClientKey != "" {
			forms++
			if c.ClientCert == "" || c.ClientKey == "" {
				errs = append(errs, i18n.New("certificate credential: clientCert and clientKey are required"))
			}
		}
		if forms != 1 {
			errs = append(errs, i18n.New("the credential must have exactly one form: header, basic or certificate"))
		}
	}
	return errs
}
