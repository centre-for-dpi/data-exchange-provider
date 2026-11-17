package config

import (
	"regexp"
	"strings"
	"time"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// InstantSpec is an instant-API service: GET by key over views of the entity's database.
type InstantSpec struct {
	Database    *Database  `yaml:"database"`
	Title       string     `yaml:"title"` // default: the service name
	Description string     `yaml:"description"`
	Version     string     `yaml:"version"` // default "1.0.0"
	Resources   []Resource `yaml:"resources"`
}

// Database is the read-only connection to the entity's database. The password lives in a file.
type Database struct {
	Engine         string        `yaml:"engine"` // sqlserver | postgres | mysql | oracle
	Host           string        `yaml:"host"`
	Port           int           `yaml:"port"`
	Name           string        `yaml:"name"`
	User           string        `yaml:"user"`
	PasswordFile   string        `yaml:"passwordFile"`
	TLS            string        `yaml:"tls"` // disable | require | verify (default require)
	CAFile         string        `yaml:"caFile"`
	MaxConnections int           `yaml:"maxConnections"` // default 4
	QueryTimeout   time.Duration `yaml:"queryTimeout"`   // default 5s
}

// Resource is one view or table exposed as GET /{name}/{key}.
type Resource struct {
	Name        string       `yaml:"name"`
	Entity      string       `yaml:"entity"`
	Source      string       `yaml:"source"` // schema.object
	Description string       `yaml:"description,omitempty"`
	Key         KeySpec      `yaml:"key"`
	Many        bool         `yaml:"many,omitempty"`
	MaxRows     int          `yaml:"maxRows,omitempty"` // default 100, 1–1000
	DataSubject *SubjectSpec `yaml:"dataSubject,omitempty"`
	Columns     []ColumnSpec `yaml:"columns"`
}

// KeySpec is the column looked up by the path parameter.
type KeySpec struct {
	Column      string `yaml:"column"`
	Pattern     string `yaml:"pattern,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// SubjectSpec declares that the key identifies a person (x-den-data-subject).
type SubjectSpec struct {
	DocumentType string `yaml:"documentType"`
}

// ColumnSpec is one exposed column; As overrides the type inferred from the catalog.
type ColumnSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	As          string `yaml:"as,omitempty"`
}

// Defaults for instant services.
const (
	DefaultDBTLS          = "require"
	DefaultMaxConnections = 4
	DefaultQueryTimeout   = 5 * time.Second
	DefaultAPIVersion     = "1.0.0"
	DefaultMaxRows        = 100
	MaxMaxRows            = 1000
)

// Engines the instant APIs support.
var Engines = []string{"sqlserver", "postgres", "mysql", "oracle"}

var (
	entityPattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	// Column names are also path parameter names and JSON properties: plain identifiers only.
	// A view can rename any other column (AS).
	columnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	asValues      = []string{"string", "integer", "number", "boolean", "date", "date-time"}
	tlsValues     = []string{"disable", "require", "verify"}
)

func (s *InstantSpec) applyDefaults(serviceName string) {
	if s.Title == "" {
		s.Title = serviceName
	}
	if s.Version == "" {
		s.Version = DefaultAPIVersion
	}
	if d := s.Database; d != nil {
		if d.TLS == "" {
			d.TLS = DefaultDBTLS
		}
		if d.MaxConnections == 0 {
			d.MaxConnections = DefaultMaxConnections
		}
		if d.QueryTimeout == 0 {
			d.QueryTimeout = DefaultQueryTimeout
		}
	}
	for i := range s.Resources {
		if s.Resources[i].MaxRows == 0 {
			s.Resources[i].MaxRows = DefaultMaxRows
		}
	}
}

// setFields names the instant fields present, to reject them in a proxy service.
func (s InstantSpec) setFields() []string {
	var out []string
	if s.Database != nil {
		out = append(out, "database")
	}
	if s.Title != "" {
		out = append(out, "title")
	}
	if s.Description != "" {
		out = append(out, "description")
	}
	if s.Version != "" {
		out = append(out, "version")
	}
	if s.Resources != nil {
		out = append(out, "resources")
	}
	return out
}

// setFields names the proxy fields present, to reject them in an instant service.
func (p ProxySpec) setFields() []string {
	var out []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"upstream", p.Upstream != ""}, {"allowPlainHttp", p.AllowPlainHTTP}, {"caFile", p.CAFile != ""},
		{"credential", p.Credential != nil}, {"maxRequestBytes", p.MaxRequestBytes != 0},
		{"connectTimeout", p.ConnectTimeout != 0}, {"responseTimeout", p.ResponseTimeout != 0},
		{"openapi", p.OpenAPI != ""},
	} {
		if f.set {
			out = append(out, f.name)
		}
	}
	return out
}

func (s InstantSpec) validate() []error {
	var errs []error
	add := func(err error) { errs = append(errs, err) }
	if s.Database == nil {
		add(i18n.Errorf("database is missing: the connection to the database (engine, host, port, name, user, passwordFile)"))
	} else {
		errs = append(errs, s.Database.validate()...)
	}
	names := map[string]bool{}
	entities := map[string]bool{}
	for i, r := range s.Resources {
		label := i18n.Sprintf("resource %d (%q)", i+1, r.Name)
		if !namePattern.MatchString(r.Name) || len(r.Name) > 80 {
			add(i18n.Errorf("%s: the name %q must use only lower-case letters, digits and hyphens (at most 80)", label, r.Name))
		} else if names[r.Name] {
			add(i18n.Errorf("%s: the name %q is repeated", label, r.Name))
		}
		names[r.Name] = true
		if !entityPattern.MatchString(r.Entity) {
			add(i18n.Errorf("%s: entity %q must start with an upper-case letter and use only letters and digits (for example Member)", label, r.Entity))
		} else if r.Entity == "Problem" {
			add(i18n.Errorf("%s: entity %q is reserved for the OpenAPI's errors; use another name", label, r.Entity))
		} else if entities[r.Entity] {
			add(i18n.Errorf("%s: the entity %q is repeated", label, r.Entity))
		}
		entities[r.Entity] = true
		if parts := strings.Split(r.Source, "."); len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			add(i18n.Errorf("%s: source %q must have the form schema.object (for example health.v_members)", label, r.Source))
		}
		if r.Key.Column == "" {
			add(i18n.Errorf("%s: key.column is missing: the column the key is looked up in", label))
		} else if !columnPattern.MatchString(r.Key.Column) {
			add(i18n.Errorf("%s: key.column %q must be a plain name (letters, digits and _); rename the column in the view", label, r.Key.Column))
		}
		if r.Key.Pattern != "" {
			if _, err := regexp.Compile(r.Key.Pattern); err != nil {
				add(i18n.Errorf("%s: key.pattern %q is not a valid regular expression", label, r.Key.Pattern))
			}
		}
		if r.MaxRows < 1 || r.MaxRows > MaxMaxRows {
			add(i18n.Errorf("%s: maxRows %d must be between 1 and %d", label, r.MaxRows, MaxMaxRows))
		}
		if r.DataSubject != nil && strings.TrimSpace(r.DataSubject.DocumentType) == "" {
			add(i18n.Errorf("%s: dataSubject.documentType cannot be empty (or remove dataSubject if the key does not identify a person)", label))
		}
		if len(r.Columns) == 0 {
			add(i18n.Errorf("%s: columns cannot be empty: list the columns to expose", label))
		}
		cols := map[string]bool{}
		for _, c := range r.Columns {
			lower := strings.ToLower(c.Name)
			switch {
			case c.Name == "":
				add(i18n.Errorf("%s: a column has no name", label))
			case !columnPattern.MatchString(c.Name):
				add(i18n.Errorf("%s: the column %q must be a plain name (letters, digits and _); rename the column in the view", label, c.Name))
			case cols[lower]:
				add(i18n.Errorf("%s: the column %q is repeated", label, c.Name))
			}
			cols[lower] = true
			if c.As != "" && !contains(asValues, c.As) {
				add(i18n.Errorf("%s: the column %q has as %q, which does not exist (%s)", label, c.Name, c.As, strings.Join(asValues, ", ")))
			}
		}
	}
	return errs
}

func (d Database) validate() []error {
	var errs []error
	var missing []string
	for _, f := range []struct {
		name string
		ok   bool
	}{
		{"engine", d.Engine != ""}, {"host", d.Host != ""}, {"port", d.Port != 0}, {"name", d.Name != ""},
		{"user", d.User != ""}, {"passwordFile", d.PasswordFile != ""},
	} {
		if !f.ok {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		errs = append(errs, i18n.Errorf("the database is missing %s", strings.Join(missing, ", ")))
	}
	if d.Engine != "" && !contains(Engines, d.Engine) {
		errs = append(errs, i18n.Errorf("the database uses engine %q, which is not supported (%s)", d.Engine, strings.Join(Engines, ", ")))
	}
	if d.Port < 0 || d.Port > 65535 {
		errs = append(errs, i18n.Errorf("the database's port %d is not valid", d.Port))
	}
	if !contains(tlsValues, d.TLS) {
		errs = append(errs, i18n.Errorf("the database's tls %q does not exist (%s)", d.TLS, strings.Join(tlsValues, ", ")))
	}
	if d.CAFile != "" && d.TLS != "verify" {
		errs = append(errs, i18n.New("the database's caFile is only used with tls: verify"))
	}
	if d.Engine == "oracle" && d.TLS == "verify" && d.CAFile != "" {
		errs = append(errs, i18n.New("with Oracle, tls: verify uses the system's CAs: remove caFile (go-ora does not take its own CA)"))
	}
	if d.MaxConnections < 0 || d.QueryTimeout < 0 {
		errs = append(errs, i18n.New("the database's maxConnections and queryTimeout cannot be negative"))
	}
	return errs
}

// Warnings are problems that do not stop the node: an instant service without resources yet
// is not served (the installer writes the connection first, then the resources).
func (n *Node) Warnings() []string {
	var out []string
	for _, s := range n.Services {
		if s.Type == "instant" && len(s.Instant.Resources) == 0 {
			out = append(out, i18n.Sprintf("service %q has no resources yet: it is not served until you add at least one", s.Name))
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
