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
)

// Patterns of the node contract v1 (heartbeat services[].name and .path).
var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	pathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~\-/]*$`)
)

// Validate returns every problem at once, in Spanish, naming the service and the field.
func (n *Node) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if err := validatePortal(n.Portal); err != nil {
		errs = append(errs, err)
	}
	if n.ControlDialAddress != "" {
		if _, _, err := net.SplitHostPort(n.ControlDialAddress); err != nil {
			add("controlDialAddress %q debe ser host:puerto", n.ControlDialAddress)
		}
	}
	errs = append(errs, validateServices(n.Services)...)
	return errors.Join(errs...)
}

// validateServices checks the services: each one, and that names and paths do not collide.
func validateServices(services []Service) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	names := map[string]bool{}
	paths := map[string]string{}
	for i, s := range services {
		label := fmt.Sprintf("servicio %d (%q)", i+1, s.Name)
		if s.File != "" {
			label = fmt.Sprintf("servicio %q (%s)", s.Name, filepath.Base(s.File))
		}
		if !namePattern.MatchString(s.Name) || len(s.Name) > 80 {
			add("%s: el nombre %q debe usar solo minúsculas, números y guiones (máximo 80)", label, s.Name)
		}
		if names[s.Name] {
			add("%s: el nombre %q está repetido", label, s.Name)
		}
		names[s.Name] = true
		if !pathPattern.MatchString(s.Path) || len(s.Path) > 200 || strings.HasSuffix(s.Path, "/") || hasDotSegment(s.Path) {
			add("%s: la ruta %q debe empezar con / seguida de un nombre, usar solo letras, números y . _ ~ - / (máximo 200, sin / final ni segmentos . o ..)", label, s.Path)
		} else {
			for other, otherName := range paths {
				switch {
				case other == s.Path:
					add("%s: la ruta %q está repetida (también la usa %q)", label, s.Path, otherName)
				case overlaps(other, s.Path):
					add("%s: la ruta %q se superpone con %q del servicio %q", label, s.Path, other, otherName)
				}
			}
			paths[s.Path] = s.Name
		}
		switch s.Type {
		case "proxy":
			if f := s.Instant.setFields(); len(f) > 0 {
				add("%s: un servicio proxy no lleva %s (son campos de las APIs instantáneas)", label, strings.Join(f, ", "))
			}
			for _, err := range s.Proxy.validate() {
				add("%s: %v", label, err)
			}
		case "instant":
			if !buildinfo.InstantSupported() {
				add("%s: el modo canal no incluye APIs instantáneas; usa la imagen completa (install.sh modo)", label)
				break
			}
			if f := s.Proxy.setFields(); len(f) > 0 {
				add("%s: un servicio instant no lleva %s (son campos de los servicios proxy)", label, strings.Join(f, ", "))
			}
			for _, err := range s.Instant.validate() {
				add("%s: %v", label, err)
			}
		default:
			add("%s: el tipo %q no existe (proxy o instant)", label, s.Type)
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
		return fmt.Errorf("portal %q debe ser una URL https://", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	dev := host == "localhost" || host == "host.docker.internal" || !strings.Contains(host, ".")
	if u.Scheme == "http" && dev {
		return nil
	}
	return fmt.Errorf("portal %q debe usar https:// (http solo para desarrollo local)", raw)
}

func (p ProxySpec) validate() []error {
	var errs []error
	u, err := url.Parse(p.Upstream)
	switch {
	case err != nil || u.Host == "":
		errs = append(errs, fmt.Errorf("upstream %q debe ser una URL http(s) completa", p.Upstream))
	case u.User != nil:
		errs = append(errs, fmt.Errorf("upstream no debe llevar usuario ni contraseña en la URL: usa credential.basic con passwordFile"))
	case u.RawQuery != "" || u.Fragment != "":
		errs = append(errs, fmt.Errorf("upstream %q no debe llevar query ni fragmento", p.Upstream))
	case u.Scheme == "http" && !p.AllowPlainHTTP:
		errs = append(errs, fmt.Errorf("upstream %q usa http sin cifrar: agrega allowPlainHttp: true si es una API interna de la LAN", p.Upstream))
	case u.Scheme != "http" && u.Scheme != "https":
		errs = append(errs, fmt.Errorf("upstream %q debe usar http o https", p.Upstream))
	}
	if p.MaxRequestBytes < 0 || p.ConnectTimeout < 0 || p.ResponseTimeout < 0 {
		errs = append(errs, errors.New("maxRequestBytes, connectTimeout y responseTimeout no pueden ser negativos"))
	}
	if c := p.Credential; c != nil {
		forms := 0
		if c.Header != "" || c.File != "" {
			forms++
			if c.Header == "" || c.File == "" {
				errs = append(errs, errors.New("credencial por cabecera: hacen falta header y file"))
			}
		}
		if c.Basic != nil {
			forms++
			if c.Basic.User == "" || c.Basic.PasswordFile == "" {
				errs = append(errs, errors.New("credencial basic: hacen falta user y passwordFile"))
			}
		}
		if c.ClientCert != "" || c.ClientKey != "" {
			forms++
			if c.ClientCert == "" || c.ClientKey == "" {
				errs = append(errs, errors.New("credencial por certificado: hacen falta clientCert y clientKey"))
			}
		}
		if forms != 1 {
			errs = append(errs, errors.New("la credencial debe tener exactamente una forma: cabecera, basic o certificado"))
		}
	}
	return errs
}
