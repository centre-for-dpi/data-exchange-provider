package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/proxy"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
	"golang.org/x/net/http/httpproxy"
)

func readErr(read func(string) ([]byte, error), path string) error {
	_, err := read(path)
	return err
}

type doctorOptions struct {
	ConfigPath, StateDir, TokenFile string
	PortalHTTP                      *http.Client
	Now                             func() time.Time
	ReadFile                        func(string) ([]byte, error) // default os.ReadFile
}

type checker struct {
	out    io.Writer
	failed bool
}

func (c *checker) ok(format string, a ...any)   { fmt.Fprintf(c.out, "✔ "+format+"\n", a...) }
func (c *checker) warn(format string, a ...any) { fmt.Fprintf(c.out, "⚠ "+format+"\n", a...) }
func (c *checker) info(format string, a ...any) { fmt.Fprintf(c.out, "ℹ "+format+"\n", a...) }
func (c *checker) fail(format string, a ...any) {
	c.failed = true
	fmt.Fprintf(c.out, "✘ "+format+"\n", a...)
}

// doctor checks what usually breaks a node in an entity's network. Exit 1 on any ✘.
func doctor(ctx context.Context, o doctorOptions, out io.Writer) int {
	c := &checker{out: out}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		c.fail("configuración: %v", err)
		return 1
	}
	c.ok("configuración válida (%d servicios)", len(cfg.Services))

	// Secrets: readable, and not readable by group or others.
	var secrets []string
	store := state.Store{Dir: o.StateDir}
	if _, err := store.Load(); errors.Is(err, state.ErrNoState) {
		secrets = append(secrets, o.TokenFile)
	}
	for _, s := range cfg.Services {
		if db := s.Instant.Database; db != nil && db.PasswordFile != "" {
			secrets = append(secrets, db.PasswordFile)
		}
		if cr := s.Proxy.Credential; cr != nil {
			for _, f := range []string{cr.File, cr.ClientKey} {
				if f != "" {
					secrets = append(secrets, f)
				}
			}
			if cr.Basic != nil {
				secrets = append(secrets, cr.Basic.PasswordFile)
			}
		}
		if s.Proxy.AllowPlainHTTP && strings.HasPrefix(s.Proxy.Upstream, "http://") {
			c.warn("el servicio %s usa http sin cifrar hacia %s (allowPlainHttp)", s.Name, s.Proxy.Upstream)
		}
	}
	for _, f := range secrets {
		info, err := os.Stat(f)
		switch {
		case errors.Is(err, fs.ErrNotExist) && f == o.TokenFile:
			c.warn("falta el token de enrolamiento en %s (el nodo esperará hasta que exista)", f)
		case err != nil:
			c.fail("secreto %s: %v", f, err)
		case readErr(o.ReadFile, f) != nil:
			c.fail("el secreto %s no lo puede leer este usuario (el contenedor corre como uid 65532): ajusta el dueño (chown 65532) o los permisos", f)
		case runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0:
			c.warn("el secreto %s lo pueden leer otros usuarios (permisos %v): usa chmod 600", f, info.Mode().Perm())
		default:
			c.ok("secreto %s legible", f)
		}
	}

	// The portal (or the corporate proxy in front of it): DNS, TCP and the clock (certificates
	// fail with a skewed clock).
	u, _ := url.Parse(cfg.Portal)
	host, port, via := u.Hostname(), u.Port(), "el portal"
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	if p, err := httpproxy.FromEnvironment().ProxyFunc()(u); err == nil && p != nil {
		host, port, via = p.Hostname(), p.Port(), "el proxy corporativo"
		if port == "" {
			port = "80"
		}
		c.ok("el portal se alcanza a través del proxy corporativo %s", p.Host)
	}
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		c.fail("DNS: no se resuelve %s (%s): %v", host, via, err)
	} else {
		c.ok("DNS resuelve %s (%s)", host, via)
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(host, port))
	cancel()
	if err != nil {
		c.fail("no hay conexión TCP con %s %s:%s: revisa la salida por 443 o el proxy corporativo", via, host, port)
	} else {
		conn.Close()
		c.ok("conexión TCP con %s %s:%s", via, host, port)
	}
	if res, err := o.PortalHTTP.Get(strings.TrimRight(cfg.Portal, "/") + "/api/v1/health"); err != nil {
		c.warn("no se pudo comparar el reloj con el portal: %v", err)
	} else {
		res.Body.Close()
		if date, err := http.ParseTime(res.Header.Get("Date")); err != nil {
			c.warn("no se pudo comparar el reloj: el portal no envió la hora (cabecera Date)")
		} else {
			skew := o.Now().Sub(date)
			if skew < 0 {
				skew = -skew
			}
			if skew > 60*time.Second {
				c.fail("el reloj del servidor está desfasado %s respecto al portal: configura NTP (timedatectl)", skew.Round(time.Second))
			} else {
				c.ok("reloj sincronizado con el portal (desfase %s)", skew.Round(time.Second))
			}
		}
	}

	// The entity's APIs.
	for _, s := range cfg.Services {
		if s.Type == "instant" {
			doctorInstant(ctx, s, c)
			continue
		}
		if err := proxy.Probe(ctx, s.Proxy); err != nil {
			c.fail("servicio %s: %v", s.Name, err)
		} else {
			c.ok("servicio %s: %s responde", s.Name, s.Proxy.Upstream)
		}
	}

	// What the running node reported.
	var st agent.Status
	if err := store.ReadStatus(&st); err != nil {
		c.warn("el nodo todavía no escribió su estado (¿está corriendo?)")
	} else {
		switch {
		case !st.UpdatedAt.IsZero() && o.Now().Sub(st.UpdatedAt) > 3*time.Minute:
			c.warn("el nodo no actualiza su estado desde hace %s: ¿está detenido? (install.sh status, install.sh logs)", o.Now().Sub(st.UpdatedAt).Round(time.Second))
		case st.PortalState == "rejected":
			c.fail("el portal rechaza el certificado del nodo: fue dado de baja o el certificado venció; usa install.sh reenrolar")
		case st.LastHandshake.IsZero() || o.Now().Sub(st.LastHandshake) > 3*time.Minute:
			c.fail("el túnel no tiene handshake reciente con el hub: ¿está bloqueada la salida por UDP 51820?")
		default:
			c.ok("túnel con handshake reciente")
		}
	}
	if c.failed {
		return 1
	}
	return 0
}
