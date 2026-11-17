//go:build !canal

package instant

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// Problem titles, shared by the handler and the OpenAPI.
const (
	titleQueryNotAllowed = "This operation does not accept parameters in the URL"
	titleInvalidKey      = "The key does not have the expected format"
	titleNotFound        = "There is no record with that key"
	titleUnavailable     = "The data source is not available"
	titleNonUnique       = "The declared key is not unique in the source"
)

// sharedTitle is one of the shared problem titles in the current language. It names each title
// here, in the file that declares it, so the catalog check can read it as a constant.
func sharedTitle(title string) string {
	switch title {
	case titleQueryNotAllowed:
		return i18n.T(titleQueryNotAllowed)
	case titleInvalidKey:
		return i18n.T(titleInvalidKey)
	case titleNotFound:
		return i18n.T(titleNotFound)
	case titleUnavailable:
		return i18n.T(titleUnavailable)
	case titleNonUnique:
		return i18n.T(titleNonUnique)
	}
	return title
}

// OpenAPI generates the service's OpenAPI 3.1 document: deterministic (sorted keys, indented),
// with one GET by key per resource, operationId get<Entity> and x-den-data-subject.
func OpenAPI(svc config.Service, rs []Compiled) ([]byte, error) {
	schemas := map[string]any{
		"Problem": map[string]any{
			"type":        "object",
			"description": i18n.T("Error in problem+json format (RFC 9457)"),
			"properties": map[string]any{
				"type": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"},
				"status": map[string]any{"type": "integer"}, "code": map[string]any{"type": "string"},
			},
			"required": []string{"code", "status", "title", "type"},
		},
	}
	problem := func(desc string) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{
			"application/problem+json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Problem"}}}}
	}
	paths := map[string]any{}
	for _, r := range rs {
		props := map[string]any{}
		var required []string
		for i, col := range r.Columns {
			spec := r.Resource.Columns[i]
			s := jsonSchema(col, r.Exposed[i])
			if spec.Description != "" {
				s["description"] = spec.Description
			}
			if col.Nullable {
				s["type"] = []any{s["type"], "null"}
			} else {
				required = append(required, spec.Name)
			}
			props[spec.Name] = s
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 { // PostgreSQL reports every view column as nullable
			sort.Strings(required)
			schema["required"] = required
		}
		schemas[r.Entity] = schema

		var body any = map[string]any{"$ref": "#/components/schemas/" + r.Entity}
		ok := i18n.T("The record with that key")
		if r.Many {
			body = map[string]any{"type": "array", "items": body, "maxItems": r.MaxRows}
			ok = i18n.Sprintf("The records with that key (at most %d)", r.MaxRows)
		}
		keySchema := jsonSchema(r.KeyColumn, r.KeyColumn.Kind)
		keySchema["pattern"] = r.Key.Pattern
		param := map[string]any{"name": r.Key.Column, "in": "path", "required": true, "schema": keySchema}
		if r.Key.Description != "" {
			param["description"] = r.Key.Description
		}
		var subject any = "none"
		if r.DataSubject != nil {
			subject = map[string]any{"in": "path", "name": r.Key.Column, "documentType": r.DataSubject.DocumentType}
		}
		op := map[string]any{
			"operationId":        "get" + r.Entity,
			"summary":            i18n.Sprintf("Get %s by %s", strings.ToLower(r.Entity), r.Key.Column),
			"tags":               []string{r.Name},
			"parameters":         []any{param},
			"x-den-data-subject": subject,
			"responses": map[string]any{
				"200": map[string]any{"description": ok, "content": map[string]any{"application/json": map[string]any{"schema": body}}},
				"400": problem(i18n.T(titleInvalidKey + ", or the URL has parameters")),
				"404": problem(sharedTitle(titleNotFound)),
				"500": problem(i18n.T("The declared key is not unique in the source, the response exceeds the records or the size the service delivers, or a source value does not fit its type")),
				"503": problem(sharedTitle(titleUnavailable)),
			},
		}
		if r.Description != "" {
			op["description"] = r.Description
		}
		paths[fmt.Sprintf("/%s/{%s}", r.Name, r.Key.Column)] = map[string]any{"get": op}
	}
	info := map[string]any{"title": svc.Instant.Title, "version": svc.Instant.Version}
	if svc.Instant.Description != "" {
		info["description"] = svc.Instant.Description
	}
	doc := map[string]any{
		"openapi":    "3.1.0",
		"info":       info,
		"paths":      paths,
		"components": map[string]any{"schemas": schemas},
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
