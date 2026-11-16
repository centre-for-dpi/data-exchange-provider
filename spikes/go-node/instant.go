// THROWAWAY SPIKE. Instant APIs, deliberately narrow: GET {resource}/{key} over a view the
// read-only user can see, and an OpenAPI generated from the same declaration. Engine specifics
// live in dialect.go; the same service.yaml works on every engine.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------- declaration (what the entity edits) ----------

type ServiceConfig struct {
	Service     string     `yaml:"service"`
	Title       string     `yaml:"title"`
	Description string     `yaml:"description"`
	Version     string     `yaml:"version"`
	Resources   []Resource `yaml:"resources"`
}

type Resource struct {
	Name        string       `yaml:"name"`   // URL segment, e.g. afiliados
	Entity      string       `yaml:"entity"` // schema name, e.g. Afiliado
	Source      string       `yaml:"source"` // schema.view (matched case-insensitively)
	Description string       `yaml:"description"`
	Key         KeySpec      `yaml:"key"`
	Many        bool         `yaml:"many"` // the key may match several rows: answer with a list
	DataSubject *SubjectSpec `yaml:"dataSubject"`
	Columns     []ColumnSpec `yaml:"columns"`
}

type KeySpec struct {
	Column      string `yaml:"column"`
	Pattern     string `yaml:"pattern"`
	Description string `yaml:"description"`
}

type SubjectSpec struct {
	DocumentType string `yaml:"documentType"`
}

type ColumnSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func loadService(path string) (*ServiceConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s ServiceConfig
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	return &s, dec.Decode(&s)
}

// ---------- catalog ----------

type CatalogObject struct {
	Schema, Name, Kind string
	Columns            []CatalogColumn
}

// Kind is the engine-neutral type: string, int, bool, decimal, date, timestamp, timestamptz, uuid,
// or "" when the column cannot be exposed (yet).
type CatalogColumn struct {
	Name, Type, Kind string
	MaxLength        int
	Nullable         bool
}

// readCatalog lists only what the connected (read-only) user can SELECT.
func readCatalog(ctx context.Context, db *sql.DB, d dialect) ([]CatalogObject, error) {
	rows, err := db.QueryContext(ctx, d.objectsQuery())
	if err != nil {
		return nil, err
	}
	var objs []CatalogObject
	for rows.Next() {
		var o CatalogObject
		if err := rows.Scan(&o.Schema, &o.Name, &o.Kind); err != nil {
			return nil, err
		}
		objs = append(objs, o)
	}
	rows.Close()
	for i := range objs {
		cols, err := db.QueryContext(ctx, d.columnsQuery(), objs[i].Schema, objs[i].Name)
		if err != nil {
			return nil, err
		}
		for cols.Next() {
			var c CatalogColumn
			var maxLen, scale sql.NullInt64
			var nullable string
			if err := cols.Scan(&c.Name, &c.Type, &maxLen, &scale, &nullable); err != nil {
				return nil, err
			}
			c.MaxLength = int(maxLen.Int64)
			c.Nullable = nullable == "Y" || nullable == "YES" || nullable == "1"
			c.Kind = d.kind(c.Type, scale)
			objs[i].Columns = append(objs[i].Columns, c)
		}
		cols.Close()
	}
	return objs, nil
}

func jsonType(c CatalogColumn) (schema map[string]any, ok bool) {
	switch c.Kind {
	case "string":
		s := map[string]any{"type": "string"}
		if c.MaxLength > 0 {
			s["maxLength"] = c.MaxLength
		}
		return s, true
	case "int":
		return map[string]any{"type": "integer"}, true
	case "bool":
		return map[string]any{"type": "boolean"}, true
	case "decimal":
		return map[string]any{"type": "number"}, true
	case "date":
		return map[string]any{"type": "string", "format": "date"}, true
	case "timestamptz":
		return map[string]any{"type": "string", "format": "date-time"}, true
	case "timestamp": // no offset in the source: not RFC 3339 date-time
		return map[string]any{"type": "string", "pattern": `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?$`}, true
	case "uuid":
		return map[string]any{"type": "string", "format": "uuid"}, true
	}
	return nil, false
}

// ---------- compiled resource (declaration validated against the catalog) ----------

type compiledResource struct {
	Resource
	query    string
	columns  []CatalogColumn // catalog columns, in declaration order
	keyCol   CatalogColumn
	keyRegex *regexp.Regexp
}

func compile(svc *ServiceConfig, catalog []CatalogObject, d dialect) ([]compiledResource, error) {
	var out []compiledResource
	for _, r := range svc.Resources {
		var obj *CatalogObject
		for i := range catalog {
			if strings.EqualFold(catalog[i].Schema+"."+catalog[i].Name, r.Source) {
				obj = &catalog[i]
			}
		}
		if obj == nil {
			return nil, fmt.Errorf("%s: %s is not readable by this user", r.Name, r.Source)
		}
		find := func(name string) (CatalogColumn, bool) {
			for _, c := range obj.Columns {
				if strings.EqualFold(c.Name, name) {
					return c, true
				}
			}
			return CatalogColumn{}, false
		}
		cr := compiledResource{Resource: r}
		key, ok := find(r.Key.Column)
		if !ok {
			return nil, fmt.Errorf("%s: key column %s not in %s", r.Name, r.Key.Column, r.Source)
		}
		cr.keyCol = key
		pattern := r.Key.Pattern
		if pattern == "" {
			if key.Kind == "int" {
				pattern = `^[0-9]{1,18}$`
			} else {
				pattern = fmt.Sprintf(`^[A-Za-z0-9-]{1,%d}$`, max(key.MaxLength, 1))
			}
		}
		cr.Key.Pattern = pattern
		cr.keyRegex = regexp.MustCompile(pattern)
		var selected []string
		for _, c := range r.Columns {
			col, ok := find(c.Name)
			if !ok {
				return nil, fmt.Errorf("%s: column %s not in %s", r.Name, c.Name, r.Source)
			}
			if _, ok := jsonType(col); !ok {
				return nil, fmt.Errorf("%s: column %s has unsupported type %s", r.Name, c.Name, col.Type)
			}
			cr.columns = append(cr.columns, col)
			selected = append(selected, d.quote(col.Name))
		}
		limit := 2 // one row expected; a second one means the key is not unique
		if r.Many {
			limit = 101
		}
		// Identifiers come from the catalog, never from the request; the key is the only parameter.
		cr.query = d.selectByKey(selected, d.quote(obj.Schema)+"."+d.quote(obj.Name), d.quote(key.Name), limit)
		out = append(out, cr)
	}
	return out, nil
}

// ---------- serving ----------

func problem(w http.ResponseWriter, status int, code, title string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": title, "status": status, "code": code})
}

func instantHandler(db *sql.DB, resources []compiledResource, servicePath string) http.Handler {
	mux := http.NewServeMux()
	for _, r := range resources {
		r := r
		template := fmt.Sprintf("%s/%s/{%s}", servicePath, r.Name, r.Key.Column)
		mux.HandleFunc(fmt.Sprintf("GET /%s/{key}", r.Name), func(w http.ResponseWriter, req *http.Request) {
			setRoute(req, template)
			if req.URL.RawQuery != "" {
				problem(w, 400, "query_not_allowed", "Esta operación no acepta parámetros en la URL")
				return
			}
			key := req.PathValue("key")
			if !r.keyRegex.MatchString(key) {
				problem(w, 400, "invalid_key", "La clave no tiene el formato esperado")
				return
			}
			var arg any = key
			if r.keyCol.Kind == "int" {
				n, err := strconv.ParseInt(key, 10, 64)
				if err != nil {
					problem(w, 400, "invalid_key", "La clave no tiene el formato esperado")
					return
				}
				arg = n
			}
			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			defer cancel()
			rows, err := db.QueryContext(ctx, r.query, arg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "db error:", err) // spike only: a real kit logs a code, not the message
				problem(w, 503, "service_unavailable", "La fuente de datos no está disponible")
				return
			}
			defer rows.Close()
			var items []map[string]any
			for rows.Next() {
				vals := make([]any, len(r.columns))
				ptrs := make([]any, len(vals))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					fmt.Fprintln(os.Stderr, "scan error:", err)
					problem(w, 503, "service_unavailable", "La fuente de datos no está disponible")
					return
				}
				item := map[string]any{}
				for i, c := range r.columns {
					item[r.Columns[i].Name] = toJSON(c, vals[i]) // JSON names are the declaration's
				}
				items = append(items, item)
			}
			switch {
			case len(items) == 0:
				problem(w, 404, "not_found", "No existe un registro con esa clave")
			case r.Many:
				writeJSON(w, items)
			case len(items) > 1:
				problem(w, 500, "non_unique_key", "La clave declarada no es única en la fuente")
			default:
				writeJSON(w, items[0])
			}
		})
	}
	// The service root answers (the portal's healthcheck GETs it); anything else undeclared: 404.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		setRoute(req, servicePath+"/")
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		setRoute(req, servicePath+"/(undeclared)")
		problem(w, 404, "not_found", "Recurso no encontrado")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func toJSON(c CatalogColumn, v any) any {
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	switch c.Kind {
	case "date":
		if t, ok := v.(time.Time); ok {
			return t.Format("2006-01-02")
		}
	case "timestamptz":
		if t, ok := v.(time.Time); ok {
			return t.Format(time.RFC3339Nano)
		}
	case "timestamp":
		if t, ok := v.(time.Time); ok {
			return t.Format("2006-01-02T15:04:05.999999999")
		}
	case "decimal":
		switch x := v.(type) {
		case string:
			return json.Number(x)
		case float64:
			return json.Number(strconv.FormatFloat(x, 'f', -1, 64))
		}
	case "int":
		switch x := v.(type) {
		case float64:
			if x == math.Trunc(x) {
				return int64(x)
			}
		case string:
			if n, err := strconv.ParseInt(x, 10, 64); err == nil {
				return n
			}
		}
	case "bool":
		switch x := v.(type) {
		case int64:
			return x != 0
		case string:
			return x == "1" || strings.EqualFold(x, "true")
		}
	}
	return v
}

// ---------- OpenAPI (generated from the same declaration; deterministic) ----------

func buildOpenAPI(svc *ServiceConfig, resources []compiledResource) ([]byte, error) {
	paths := map[string]any{}
	schemas := map[string]any{
		"Problem": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"},
				"status": map[string]any{"type": "integer"}, "code": map[string]any{"type": "string"},
			},
			"required": []string{"type", "title", "status", "code"},
		},
	}
	problemResponse := func(desc string) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{
			"application/problem+json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Problem"}}}}
	}
	for _, r := range resources {
		props := map[string]any{}
		var required []string
		for i, c := range r.columns {
			name := r.Columns[i].Name
			s, _ := jsonType(c)
			if d := r.Columns[i].Description; d != "" {
				s["description"] = d
			}
			if c.Nullable {
				s["type"] = []any{s["type"], "null"}
			} else {
				required = append(required, name)
			}
			props[name] = s
		}
		sort.Strings(required)
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 { // PostgreSQL reports every view column as nullable
			schema["required"] = required
		}
		schemas[r.Entity] = schema
		var body any = map[string]any{"$ref": "#/components/schemas/" + r.Entity}
		if r.Many {
			body = map[string]any{"type": "array", "items": body}
		}
		keySchema, _ := jsonType(r.keyCol)
		keySchema["pattern"] = r.Key.Pattern
		var subject any = "none"
		if r.DataSubject != nil {
			subject = map[string]any{"in": "path", "name": r.Key.Column, "documentType": r.DataSubject.DocumentType}
		}
		paths[fmt.Sprintf("/%s/{%s}", r.Name, r.Key.Column)] = map[string]any{
			"get": map[string]any{
				"operationId": "get" + r.Entity,
				"summary":     fmt.Sprintf("Consultar %s por %s", strings.ToLower(r.Entity), r.Key.Column),
				"description": r.Description,
				"tags":        []string{r.Name},
				"parameters": []any{map[string]any{
					"name": r.Key.Column, "in": "path", "required": true,
					"description": r.Key.Description, "schema": keySchema,
				}},
				"x-rid-data-subject": subject,
				"responses": map[string]any{
					"200": map[string]any{"description": "El registro", "content": map[string]any{"application/json": map[string]any{"schema": body}}},
					"400": problemResponse("Clave con formato inválido o parámetros no permitidos"),
					"404": problemResponse("No existe un registro con esa clave"),
					"503": problemResponse("La fuente de datos no está disponible"),
				},
			},
		}
	}
	doc := map[string]any{
		"openapi":    "3.1.0",
		"info":       map[string]any{"title": svc.Title, "version": svc.Version, "description": svc.Description},
		"paths":      paths,
		"components": map[string]any{"schemas": schemas},
	}
	return json.MarshalIndent(doc, "", "  ")
}
