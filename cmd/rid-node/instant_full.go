//go:build !canal

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/data-exchange-provider/internal/agent"
	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/contract"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant"
	"github.com/centre-for-dpi/data-exchange-provider/internal/instant/dialect"
	"github.com/centre-for-dpi/data-exchange-provider/internal/server"
)

// dialectFor picks the engine's dialect (tests swap it for a fake database).
var dialectFor = dialect.For

// cliTimeout bounds what a command waits for the database.
const cliTimeout = 30 * time.Second

func openInstant(s config.Service, log *slog.Logger) (*instant.Service, error) {
	d, err := dialectFor(s.Instant.Database.Engine)
	if err != nil {
		return nil, err
	}
	return instant.Open(s, d, log)
}

// instantRuntime opens and starts an instant service for run: it serves 503 until its catalog
// compiles, and the heartbeat carries its OpenAPI's SHA once it does.
func instantRuntime(ctx context.Context, s config.Service, log *slog.Logger) (server.Route, agent.ServiceRuntime, error) {
	svc, err := openInstant(s, log)
	if err != nil {
		return server.Route{}, agent.ServiceRuntime{}, err
	}
	svc.Start(ctx)
	go func() { <-ctx.Done(); _ = svc.Close() }()
	sha := func() string {
		b, err := svc.OpenAPI()
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	return server.Route{Path: s.Path, Handler: svc.Handler()},
		agent.ServiceRuntime{Info: contract.ServiceInfo{Name: s.Name, Path: s.Path}, Probe: svc.Probe, OpenAPISHA256: sha}, nil
}

// detail adds the driver's message to a database error, for the operator's terminal only
// (never for the logs).
func detail(err error) string {
	var de *instant.DBError
	if errors.As(err, &de) && errors.Unwrap(de) != nil {
		return fmt.Sprintf("%v (%v)", err, errors.Unwrap(de))
	}
	return err.Error()
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// validateInstant compiles an instant service when its database answers; a database that does
// not answer is only a warning (the node retries when it runs).
func validateInstant(ctx context.Context, s config.Service, out io.Writer) bool {
	svc, err := openInstant(s, quietLog())
	if err != nil {
		fmt.Fprintf(out, "✘ servicio %q: %v\n", s.Name, err)
		return false
	}
	defer svc.Close()
	if len(s.Instant.Resources) == 0 {
		return true // the warning was already printed
	}
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	var de *instant.DBError
	switch err := svc.Compile(ctx); {
	case errors.As(err, &de):
		fmt.Fprintf(out, "⚠ servicio %q: no se pudo revisar contra la base: %s (el nodo reintentará al arrancar)\n", s.Name, detail(err))
	case err != nil:
		fmt.Fprintf(out, "✘ servicio %q: la declaración no compila contra la base:\n%v\n", s.Name, err)
		return false
	default:
		fmt.Fprintf(out, "✔ servicio %q: compila contra la base (%d recursos)\n", s.Name, len(s.Instant.Resources))
	}
	return true
}

// doctorInstant: connection, catalog, write privileges and the read-only transaction.
func doctorInstant(ctx context.Context, s config.Service, c *checker) {
	svc, err := openInstant(s, quietLog())
	if err != nil {
		c.fail("servicio %s: %v", s.Name, err)
		return
	}
	defer svc.Close()
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	db := s.Instant.Database
	var de *instant.DBError
	err = svc.Compile(ctx)
	if errors.As(err, &de) {
		c.fail("servicio %s: %s en %s:%d", s.Name, detail(err), db.Host, db.Port)
		return
	}
	c.ok("servicio %s: conexión con la base de datos %s:%d (%s)", s.Name, db.Host, db.Port, db.Engine)
	switch {
	case len(s.Instant.Resources) == 0:
		c.warn("servicio %s: todavía no tiene recursos (install.sh servicio)", s.Name)
		return
	case err != nil:
		c.fail("servicio %s: la declaración no compila contra la base:\n%v", s.Name, err)
		return
	}
	c.ok("servicio %s: el catálogo compila (%d recursos)", s.Name, len(s.Instant.Resources))
	writable, err := svc.WriteCheck(ctx)
	switch {
	case err != nil:
		c.warn("servicio %s: no se pudo revisar si el usuario puede escribir: %s", s.Name, detail(err))
	case len(writable) > 0:
		c.fail("servicio %s: el usuario %s puede escribir en %s: usa un usuario de solo lectura (solo SELECT sobre las vistas)", s.Name, db.User, strings.Join(writable, ", "))
	default:
		c.ok("servicio %s: el usuario %s solo puede leer", s.Name, db.User)
	}
	if db.Engine == "sqlserver" {
		c.info("servicio %s: SQL Server no tiene transacciones de solo lectura; la garantía es el usuario de solo lectura", s.Name)
	} else {
		c.ok("servicio %s: las consultas corren en una transacción de solo lectura", s.Name)
	}
}

// instantCommand runs catalog, scaffold and openapi.
func instantCommand(name string, args []string, configPath string, out io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	switch name {
	case "catalog":
		return catalogCmd(ctx, args, out)
	case "scaffold":
		return scaffoldCmd(ctx, args, out)
	case "openapi":
		return openapiCmd(ctx, args, configPath, out)
	}
	return 2
}

func serviceFileFlag(fs *flag.FlagSet) *string {
	return fs.String("service-file", "", "archivo del servicio (config/services/<nombre>.yaml)")
}

func loadInstantFile(path string, out io.Writer) (config.Service, bool) {
	if path == "" {
		fmt.Fprintln(out, "✘ falta --service-file")
		return config.Service{}, false
	}
	s, err := config.LoadServiceFile(path)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return config.Service{}, false
	}
	if s.Type != "instant" {
		fmt.Fprintf(out, "✘ %s no es un servicio instant\n", path)
		return config.Service{}, false
	}
	return s, true
}

func catalogCmd(ctx context.Context, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("catalog", flag.ContinueOnError)
	fs.SetOutput(out)
	file := serviceFileFlag(fs)
	if fs.Parse(args) != nil {
		return 2
	}
	s, ok := loadInstantFile(*file, out)
	if !ok {
		return 1
	}
	svc, err := openInstant(s, quietLog())
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	defer svc.Close()
	cat, err := svc.Catalog(ctx)
	if err != nil {
		fmt.Fprintf(out, "✘ %s\n", detail(err))
		return 1
	}
	if len(cat) == 0 {
		fmt.Fprintf(out, "el usuario %s no puede leer ninguna vista ni tabla: dale SELECT sobre las vistas que vas a exponer\n", s.Instant.Database.User)
		return 1
	}
	for _, o := range cat {
		fmt.Fprintf(out, "%s.%s (%s)\n", o.Schema, o.Name, map[string]string{"view": "vista", "table": "tabla"}[o.Kind])
		for _, c := range o.Columns {
			kind := string(c.Kind)
			if c.Kind == instant.KindUnsupported {
				kind = "✘ no se puede exponer"
			}
			fmt.Fprintf(out, "  %-32s %-28s %s\n", c.Name, c.Native, kind)
		}
	}
	return 0
}

func scaffoldCmd(ctx context.Context, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("scaffold", flag.ContinueOnError)
	fs.SetOutput(out)
	file := serviceFileFlag(fs)
	var o instant.ScaffoldOptions
	fs.StringVar(&o.Source, "source", "", "vista o tabla, esquema.objeto")
	fs.StringVar(&o.Key, "key", "", "columna que se busca con la clave")
	fs.StringVar(&o.Resource, "resource", "", "nombre del recurso en la URL (por defecto, el del objeto)")
	fs.StringVar(&o.Entity, "entity", "", "nombre de la entidad en el OpenAPI (por defecto, el del recurso)")
	fs.StringVar(&o.Subject, "subject", "", "tipo de documento si la clave identifica a una persona")
	fs.BoolVar(&o.Many, "many", false, "la clave puede traer varias filas")
	if fs.Parse(args) != nil {
		return 2
	}
	if o.Source == "" || o.Key == "" {
		fmt.Fprintln(out, "✘ faltan --source y --key")
		return 2
	}
	s, ok := loadInstantFile(*file, out)
	if !ok {
		return 1
	}
	d, err := dialectFor(s.Instant.Database.Engine)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	svc, err := instant.Open(s, d, quietLog())
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	defer svc.Close()
	cat, err := svc.Catalog(ctx)
	if err != nil {
		fmt.Fprintf(out, "✘ %s\n", detail(err))
		return 1
	}
	r, skipped, err := instant.Scaffold(cat, o)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	for _, existing := range s.Instant.Resources {
		if existing.Name == r.Name {
			fmt.Fprintf(out, "✘ el servicio ya tiene un recurso %q: usa --resource con otro nombre\n", r.Name)
			return 1
		}
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	updated, err := appendResource(raw, r)
	if err != nil {
		fmt.Fprintf(out, "✘ no se pudo escribir el recurso: %v\n", err)
		return 1
	}
	// Check the result (configuration and catalog) before replacing the file.
	tmp := filepath.Join(filepath.Dir(*file), "."+filepath.Base(*file)+".tmp")
	info, _ := os.Stat(*file)
	mode := os.FileMode(0o644)
	if info != nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(tmp, updated, mode); err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	defer os.Remove(tmp)
	next, err := config.LoadServiceFile(tmp)
	if err == nil {
		_, err = instant.Compile(next, cat, d)
	}
	if err != nil {
		fmt.Fprintf(out, "✘ el recurso no es válido:\n%s\n", strings.ReplaceAll(err.Error(), tmp, *file))
		return 1
	}
	if err := os.Rename(tmp, *file); err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "✔ recurso %q agregado: GET %s/%s/{%s} (%d columnas)\n", r.Name, s.Path, r.Name, r.Key.Column, len(r.Columns))
	for _, sk := range skipped {
		fmt.Fprintf(out, "⚠ columna omitida: %s\n", sk)
	}
	return 0
}

// appendResource adds a resource to the service file's resources, keeping the rest as written.
func appendResource(raw []byte, r config.Resource) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("el archivo no es un mapa YAML")
	}
	var rn yaml.Node
	if err := rn.Encode(r); err != nil {
		return nil, err
	}
	m := doc.Content[0]
	var seq *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "resources" {
			seq = m.Content[i+1]
			if seq.Kind != yaml.SequenceNode {
				*seq = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			}
			seq.Style = 0 // block
		}
	}
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "resources"}, seq)
	}
	seq.Content = append(seq.Content, &rn)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

func openapiCmd(ctx context.Context, args []string, configPath string, out io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(out, "uso: rid-node openapi <servicio>")
		return 2
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	for _, s := range cfg.Services {
		if s.Name != args[0] {
			continue
		}
		if s.Type != "instant" {
			fmt.Fprintf(out, "✘ el servicio %q no es instant: su OpenAPI lo escribe la entidad\n", s.Name)
			return 1
		}
		svc, err := openInstant(s, quietLog())
		if err != nil {
			fmt.Fprintf(out, "✘ %v\n", err)
			return 1
		}
		defer svc.Close()
		if err := svc.Compile(ctx); err != nil {
			fmt.Fprintf(out, "✘ %s\n", detail(err))
			return 1
		}
		b, err := svc.OpenAPI()
		if err != nil {
			fmt.Fprintf(out, "✘ %v\n", err)
			return 1
		}
		_, _ = out.Write(b)
		return 0
	}
	fmt.Fprintf(out, "✘ no hay un servicio %q\n", args[0])
	return 1
}
