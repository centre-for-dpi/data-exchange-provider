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
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
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
// compiles, and the heartbeat carries its OpenAPI's SHA once it does. closeFn closes the pool;
// run calls it after the server has drained.
func instantRuntime(ctx context.Context, s config.Service, log *slog.Logger) (route server.Route, rt agent.ServiceRuntime, closeFn func(), err error) {
	svc, err := openInstant(s, log)
	if err != nil {
		return server.Route{}, agent.ServiceRuntime{}, nil, err
	}
	svc.Start(ctx)
	sha := func() string {
		b, err := svc.OpenAPI()
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	return server.Route{Path: s.Path, Handler: svc.Handler()},
		agent.ServiceRuntime{Info: contract.ServiceInfo{Name: s.Name, Path: s.Path}, Probe: svc.Probe, OpenAPISHA256: sha},
		func() { _ = svc.Close() }, nil
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
		i18n.Fprintf(out, "✘ service %q: %v\n", s.Name, err)
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
		i18n.Fprintf(out, "⚠ service %q: could not check against the database: %s (the node will retry when it starts)\n", s.Name, detail(err))
	case err != nil:
		i18n.Fprintf(out, "✘ service %q: the declaration does not compile against the database:\n%v\n", s.Name, err)
		return false
	default:
		i18n.Fprintf(out, "✔ service %q: compiles against the database (%d resources)\n", s.Name, len(s.Instant.Resources))
	}
	return true
}

// doctorInstant: connection, catalog, write privileges and the read-only transaction.
func doctorInstant(ctx context.Context, s config.Service, c *checker) {
	svc, err := openInstant(s, quietLog())
	if err != nil {
		c.fail(i18n.Sprintf("service %s: %v", s.Name, err))
		return
	}
	defer svc.Close()
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	db := s.Instant.Database
	var de *instant.DBError
	err = svc.Compile(ctx)
	if errors.As(err, &de) {
		c.fail(i18n.Sprintf("service %s: %s at %s:%d", s.Name, detail(err), db.Host, db.Port))
		return
	}
	server := db.Engine
	if v, err := svc.ServerVersion(ctx); err == nil && v != "" {
		server = v
	}
	c.ok(i18n.Sprintf("service %s: connected to the database %s:%d (%s)", s.Name, db.Host, db.Port, server))
	switch {
	case len(s.Instant.Resources) == 0:
		c.warn(i18n.Sprintf("service %s: has no resources yet (install.sh service)", s.Name))
		return
	case err != nil:
		c.fail(i18n.Sprintf("service %s: the declaration does not compile against the database:\n%v", s.Name, err))
		return
	}
	c.ok(i18n.Sprintf("service %s: the catalog compiles (%d resources)", s.Name, len(s.Instant.Resources)))
	writable, err := svc.WriteCheck(ctx)
	switch {
	case err != nil:
		c.warn(i18n.Sprintf("service %s: could not check whether the user can write: %s", s.Name, detail(err)))
	case len(writable) > 0:
		c.fail(i18n.Sprintf("service %s: the user %s can write to %s: use a read-only user (only SELECT on the views)", s.Name, db.User, strings.Join(writable, ", ")))
	default:
		c.ok(i18n.Sprintf("service %s: the user %s cannot write to the exposed sources (%d)", s.Name, db.User, len(s.Instant.Resources)))
	}
	switch err := svc.CheckReadOnly(ctx); {
	case db.Engine == "sqlserver":
		c.info(i18n.Sprintf("service %s: SQL Server has no read-only transactions; the guarantee is the read-only user", s.Name))
	case err != nil:
		c.fail(i18n.Sprintf("service %s: could not open a read-only transaction: %s", s.Name, detail(err)))
	default:
		c.ok(i18n.Sprintf("service %s: queries run in a read-only transaction (opened and closed just now)", s.Name))
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
	return fs.String("service-file", "", i18n.T("the service's file (config/services/<name>.yaml)"))
}

func loadInstantFile(path string, out io.Writer) (config.Service, bool) {
	if path == "" {
		i18n.Fprintln(out, "✘ --service-file is missing")
		return config.Service{}, false
	}
	s, err := config.LoadServiceFile(path)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return config.Service{}, false
	}
	if s.Type != "instant" {
		i18n.Fprintf(out, "✘ %s is not an instant service\n", path)
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
	if v, err := svc.ServerVersion(ctx); err == nil && v != "" {
		i18n.Fprintf(out, "database: %s\n", v)
	}
	if len(cat) == 0 {
		i18n.Fprintf(out, "the user %s cannot read any view or table: grant it SELECT on the views you will expose\n", s.Instant.Database.User)
		return 1
	}
	for _, o := range cat {
		fmt.Fprintf(out, "%s.%s (%s)\n", o.Schema, o.Name, map[string]string{"view": i18n.T("view"), "table": i18n.T("table")}[o.Kind])
		for _, c := range o.Columns {
			kind := string(c.Kind)
			if c.Kind == instant.KindUnsupported {
				kind = i18n.T("✘ cannot be exposed")
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
	fs.StringVar(&o.Source, "source", "", i18n.T("view or table, schema.object"))
	fs.StringVar(&o.Key, "key", "", i18n.T("the column the key is looked up in"))
	fs.StringVar(&o.Resource, "resource", "", i18n.T("the resource's name in the URL (default: the object's)"))
	fs.StringVar(&o.Entity, "entity", "", i18n.T("the entity's name in the OpenAPI (default: the resource's)"))
	fs.StringVar(&o.Subject, "subject", "", i18n.T("document type if the key identifies a person"))
	fs.BoolVar(&o.Many, "many", false, i18n.T("the key may return several rows"))
	printOnly := fs.Bool("print", false, i18n.T("prints the resulting file instead of writing it (install.sh)"))
	if fs.Parse(args) != nil {
		return 2
	}
	if o.Source == "" || o.Key == "" {
		i18n.Fprintln(out, "✘ --source and --key are missing")
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
			i18n.Fprintf(out, "✘ the service already has a resource %q: use --resource with another name\n", r.Name)
			return 1
		}
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	comment := ""
	if len(skipped) > 0 {
		comment = i18n.Sprintf("Columns skipped by den-node scaffold:\n- %s", strings.Join(skipped, "\n- "))
	}
	updated, err := appendResource(raw, r, comment)
	if err != nil {
		i18n.Fprintf(out, "✘ could not write the resource: %v\n", err)
		return 1
	}
	// Check the result (configuration and catalog) before anything is written.
	next, err := config.ParseService(updated, *file)
	if err == nil {
		_, err = instant.Compile(next, cat, d)
	}
	if err != nil {
		i18n.Fprintf(out, "✘ the resource is not valid:\n%v\n", err)
		return 1
	}
	if *printOnly {
		_, _ = out.Write(updated)
		return 0
	}
	tmp := filepath.Join(filepath.Dir(*file), "."+filepath.Base(*file)+".tmp")
	mode := os.FileMode(0o644)
	if info, err := os.Stat(*file); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(tmp, updated, mode); err != nil {
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	if err := os.Rename(tmp, *file); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(out, "✘ %v\n", err)
		return 1
	}
	i18n.Fprintf(out, "✔ resource %q added: GET %s/%s/{%s} (%d columns)\n", r.Name, s.Path, r.Name, r.Key.Column, len(r.Columns))
	for _, sk := range skipped {
		i18n.Fprintf(out, "⚠ column skipped: %s\n", sk)
	}
	return 0
}

// appendResource adds a resource to the service file's resources, keeping the rest as written;
// comment (if any) goes over the new resource.
func appendResource(raw []byte, r config.Resource, comment string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, i18n.New("the file is not a YAML map")
	}
	var rn yaml.Node
	if err := rn.Encode(r); err != nil {
		return nil, err
	}
	rn.HeadComment = comment
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
		i18n.Fprintln(out, "usage: den-node openapi <service>")
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
			i18n.Fprintf(out, "✘ service %q is not instant: the entity writes its OpenAPI\n", s.Name)
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
	i18n.Fprintf(out, "✘ there is no service %q\n", args[0])
	return 1
}
