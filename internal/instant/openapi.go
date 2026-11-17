//go:build !canal

package instant

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

// Problem titles, shared by the handler and the OpenAPI.
const (
	titleQueryNotAllowed = "Esta operación no acepta parámetros en la URL"
	titleInvalidKey      = "La clave no tiene el formato esperado"
	titleNotFound        = "No existe un registro con esa clave"
	titleUnavailable     = "La fuente de datos no está disponible"
	titleNonUnique       = "La clave declarada no es única en la fuente"
)

// OpenAPI generates the service's OpenAPI 3.1 document: deterministic (sorted keys, indented),
// with one GET by key per resource, operationId get<Entity> and x-rid-data-subject.
func OpenAPI(svc config.Service, rs []Compiled) ([]byte, error) {
	schemas := map[string]any{
		"Problem": map[string]any{
			"type":        "object",
			"description": "Error en formato problem+json (RFC 9457)",
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
		ok := "El registro con esa clave"
		if r.Many {
			body = map[string]any{"type": "array", "items": body, "maxItems": r.MaxRows}
			ok = fmt.Sprintf("Los registros con esa clave (máximo %d)", r.MaxRows)
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
			"summary":            fmt.Sprintf("Consultar %s por %s", strings.ToLower(r.Entity), r.Key.Column),
			"tags":               []string{r.Name},
			"parameters":         []any{param},
			"x-rid-data-subject": subject,
			"responses": map[string]any{
				"200": map[string]any{"description": ok, "content": map[string]any{"application/json": map[string]any{"schema": body}}},
				"400": problem(titleInvalidKey + ", o la URL trae parámetros"),
				"404": problem(titleNotFound),
				"500": problem("La clave declarada no es única en la fuente, o la respuesta pasa de los registros o del tamaño que el servicio entrega"),
				"503": problem(titleUnavailable),
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
