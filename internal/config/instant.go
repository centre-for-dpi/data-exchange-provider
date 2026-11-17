package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
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
	Description string       `yaml:"description"`
	Key         KeySpec      `yaml:"key"`
	Many        bool         `yaml:"many"`
	MaxRows     int          `yaml:"maxRows"` // default 100, 1–1000
	DataSubject *SubjectSpec `yaml:"dataSubject"`
	Columns     []ColumnSpec `yaml:"columns"`
}

// KeySpec is the column looked up by the path parameter.
type KeySpec struct {
	Column      string `yaml:"column"`
	Pattern     string `yaml:"pattern"`
	Description string `yaml:"description"`
}

// SubjectSpec declares that the key identifies a person (x-rid-data-subject).
type SubjectSpec struct {
	DocumentType string `yaml:"documentType"`
}

// ColumnSpec is one exposed column; As overrides the type inferred from the catalog.
type ColumnSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	As          string `yaml:"as"`
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
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Database == nil {
		add("falta database: la conexión a la base de datos (engine, host, port, name, user, passwordFile)")
	} else {
		errs = append(errs, s.Database.validate()...)
	}
	names := map[string]bool{}
	entities := map[string]bool{}
	for i, r := range s.Resources {
		label := fmt.Sprintf("recurso %d (%q)", i+1, r.Name)
		if !namePattern.MatchString(r.Name) || len(r.Name) > 80 {
			add("%s: el nombre %q debe usar solo minúsculas, números y guiones (máximo 80)", label, r.Name)
		} else if names[r.Name] {
			add("%s: el nombre %q está repetido", label, r.Name)
		}
		names[r.Name] = true
		if !entityPattern.MatchString(r.Entity) {
			add("%s: entity %q debe empezar con mayúscula y usar solo letras y números (por ejemplo Afiliado)", label, r.Entity)
		} else if r.Entity == "Problem" {
			add("%s: entity %q está reservada para los errores del OpenAPI; usa otro nombre", label, r.Entity)
		} else if entities[r.Entity] {
			add("%s: la entidad %q está repetida", label, r.Entity)
		}
		entities[r.Entity] = true
		if parts := strings.Split(r.Source, "."); len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			add("%s: source %q debe tener la forma esquema.objeto (por ejemplo salud.v_afiliados)", label, r.Source)
		}
		if r.Key.Column == "" {
			add("%s: falta key.column, la columna que se busca con la clave", label)
		} else if !columnPattern.MatchString(r.Key.Column) {
			add("%s: key.column %q debe ser un nombre simple (letras, números y _); renombra la columna en la vista", label, r.Key.Column)
		}
		if r.Key.Pattern != "" {
			if _, err := regexp.Compile(r.Key.Pattern); err != nil {
				add("%s: key.pattern %q no es una expresión regular válida", label, r.Key.Pattern)
			}
		}
		if r.MaxRows < 1 || r.MaxRows > MaxMaxRows {
			add("%s: maxRows %d debe estar entre 1 y %d", label, r.MaxRows, MaxMaxRows)
		}
		if r.DataSubject != nil && strings.TrimSpace(r.DataSubject.DocumentType) == "" {
			add("%s: dataSubject.documentType no puede estar vacío (o quita dataSubject si la clave no identifica a una persona)", label)
		}
		if len(r.Columns) == 0 {
			add("%s: columns no puede estar vacío: lista las columnas que se exponen", label)
		}
		cols := map[string]bool{}
		for _, c := range r.Columns {
			lower := strings.ToLower(c.Name)
			switch {
			case c.Name == "":
				add("%s: una columna no tiene name", label)
			case !columnPattern.MatchString(c.Name):
				add("%s: la columna %q debe ser un nombre simple (letras, números y _); renombra la columna en la vista", label, c.Name)
			case cols[lower]:
				add("%s: la columna %q está repetida", label, c.Name)
			}
			cols[lower] = true
			if c.As != "" && !contains(asValues, c.As) {
				add("%s: la columna %q tiene as %q, que no existe (%s)", label, c.Name, c.As, strings.Join(asValues, ", "))
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
		errs = append(errs, fmt.Errorf("a la base de datos le falta %s", strings.Join(missing, ", ")))
	}
	if d.Engine != "" && !contains(Engines, d.Engine) {
		errs = append(errs, fmt.Errorf("la base de datos usa engine %q, que no está soportado (%s)", d.Engine, strings.Join(Engines, ", ")))
	}
	if d.Port < 0 || d.Port > 65535 {
		errs = append(errs, fmt.Errorf("el puerto %d de la base de datos no es válido", d.Port))
	}
	if !contains(tlsValues, d.TLS) {
		errs = append(errs, fmt.Errorf("tls %q de la base de datos no existe (%s)", d.TLS, strings.Join(tlsValues, ", ")))
	}
	if d.CAFile != "" && d.TLS != "verify" {
		errs = append(errs, errors.New("caFile de la base de datos solo se usa con tls: verify"))
	}
	if d.Engine == "oracle" && d.TLS == "verify" && d.CAFile != "" {
		errs = append(errs, errors.New("con Oracle, tls: verify usa las CA del sistema: quita caFile (go-ora no acepta una CA propia)"))
	}
	if d.MaxConnections < 0 || d.QueryTimeout < 0 {
		errs = append(errs, errors.New("maxConnections y queryTimeout de la base de datos no pueden ser negativos"))
	}
	return errs
}

// Warnings are problems that do not stop the node: an instant service without resources yet
// is not served (the installer writes the connection first, then the resources).
func (n *Node) Warnings() []string {
	var out []string
	for _, s := range n.Services {
		if s.Type == "instant" && len(s.Instant.Resources) == 0 {
			out = append(out, fmt.Sprintf("el servicio %q todavía no tiene recursos: no se sirve hasta que agregues al menos uno", s.Name))
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
