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
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
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

// checker prints one line per check; each message comes already in the operator's language.
type checker struct {
	out    io.Writer
	failed bool
}

func (c *checker) ok(msg string)   { fmt.Fprintln(c.out, "✔ "+msg) }
func (c *checker) warn(msg string) { fmt.Fprintln(c.out, "⚠ "+msg) }
func (c *checker) info(msg string) { fmt.Fprintln(c.out, "ℹ "+msg) }
func (c *checker) fail(msg string) {
	c.failed = true
	fmt.Fprintln(c.out, "✘ "+msg)
}

// doctor checks what usually breaks a node in an entity's network. Exit 1 on any ✘.
func doctor(ctx context.Context, o doctorOptions, out io.Writer) int {
	c := &checker{out: out}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		c.fail(i18n.Sprintf("configuration: %v", err))
		return 1
	}
	c.ok(i18n.Sprintf("valid configuration (%d services)", len(cfg.Services)))

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
			c.warn(i18n.Sprintf("service %s uses unencrypted http to %s (allowPlainHttp)", s.Name, s.Proxy.Upstream))
		}
	}
	for _, f := range secrets {
		info, err := os.Stat(f)
		switch {
		case errors.Is(err, fs.ErrNotExist) && f == o.TokenFile:
			c.warn(i18n.Sprintf("the enrollment token is missing at %s (the node will wait until it exists)", f))
		case err != nil:
			c.fail(i18n.Sprintf("secret %s: %v", f, err))
		case readErr(o.ReadFile, f) != nil:
			c.fail(i18n.Sprintf("this user cannot read the secret %s (the container runs as uid 65532): fix its owner (chown 65532) or its permissions", f))
		case runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0:
			c.warn(i18n.Sprintf("other users can read the secret %s (permissions %v): use chmod 600", f, info.Mode().Perm()))
		default:
			c.ok(i18n.Sprintf("secret %s readable", f))
		}
	}

	// The portal (or the corporate proxy in front of it): DNS, TCP and the clock (certificates
	// fail with a skewed clock).
	u, _ := url.Parse(cfg.Portal)
	host, port, via := u.Hostname(), u.Port(), i18n.T("the portal")
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	if p, err := httpproxy.FromEnvironment().ProxyFunc()(u); err == nil && p != nil {
		host, port, via = p.Hostname(), p.Port(), i18n.T("the corporate proxy")
		if port == "" {
			port = "80"
		}
		c.ok(i18n.Sprintf("the portal is reached through the corporate proxy %s", p.Host))
	}
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		c.fail(i18n.Sprintf("DNS: %s does not resolve (%s): %v", host, via, err))
	} else {
		c.ok(i18n.Sprintf("DNS resolves %s (%s)", host, via))
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(host, port))
	cancel()
	if err != nil {
		c.fail(i18n.Sprintf("no TCP connection to %s %s:%s: check outbound traffic on 443 or the corporate proxy", via, host, port))
	} else {
		conn.Close()
		c.ok(i18n.Sprintf("TCP connection to %s %s:%s", via, host, port))
	}
	if res, err := o.PortalHTTP.Get(strings.TrimRight(cfg.Portal, "/") + "/api/v1/health"); err != nil {
		c.warn(i18n.Sprintf("could not compare the clock with the portal: %v", err))
	} else {
		res.Body.Close()
		if date, err := http.ParseTime(res.Header.Get("Date")); err != nil {
			c.warn(i18n.T("could not compare the clock: the portal did not send the time (Date header)"))
		} else {
			skew := o.Now().Sub(date)
			if skew < 0 {
				skew = -skew
			}
			if skew > 60*time.Second {
				c.fail(i18n.Sprintf("the server's clock is %s off from the portal: set up NTP (timedatectl)", skew.Round(time.Second)))
			} else {
				c.ok(i18n.Sprintf("clock in sync with the portal (skew %s)", skew.Round(time.Second)))
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
			c.fail(i18n.Sprintf("service %s: %v", s.Name, err))
		} else {
			c.ok(i18n.Sprintf("service %s: %s answers", s.Name, s.Proxy.Upstream))
		}
	}

	// What the running node reported.
	var st agent.Status
	if err := store.ReadStatus(&st); err != nil {
		c.warn(i18n.T("the node has not written its status yet (is it running?)"))
	} else {
		switch {
		case !st.UpdatedAt.IsZero() && o.Now().Sub(st.UpdatedAt) > 3*time.Minute:
			c.warn(i18n.Sprintf("the node has not updated its status for %s: is it stopped? (install.sh status, install.sh logs)", o.Now().Sub(st.UpdatedAt).Round(time.Second)))
		case st.PortalState == "rejected":
			c.fail(i18n.T("the portal rejects the node's certificate: the node was removed or the certificate expired; use install.sh reenroll"))
		case st.LastHandshake.IsZero() || o.Now().Sub(st.LastHandshake) > 3*time.Minute:
			c.fail(i18n.T("the tunnel has no recent handshake with the hub: is outbound UDP 51820 blocked?"))
		default:
			c.ok(i18n.T("tunnel with a recent handshake"))
		}
	}
	if c.failed {
		return 1
	}
	return 0
}
