//go:build !canal

package instant

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

// ScaffoldOptions are the choices for a new resource; empty Resource and Entity are derived
// from the object's name.
type ScaffoldOptions struct {
	Source, Key      string
	Resource, Entity string
	Subject          string // documentType; empty: the key does not identify a person
	Many             bool
}

var (
	resourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	entityName   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	plainColumn  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// Scaffold proposes a resource over a catalog object with every column the kit can expose, in
// catalog order and in lower case. It returns the columns it left out and why.
func Scaffold(catalog []Object, o ScaffoldOptions) (config.Resource, []string, error) {
	obj, err := findObject(catalog, o.Source)
	if err != nil {
		return config.Resource{}, nil, err
	}
	name := o.Resource
	if name == "" {
		name = strings.ReplaceAll(strings.TrimPrefix(strings.ToLower(obj.Name), "v_"), "_", "-")
	}
	if !resourceName.MatchString(name) || len(name) > 80 {
		return config.Resource{}, nil, fmt.Errorf("el nombre de recurso %q no sirve en una URL: usa --resource con minúsculas, números y guiones", name)
	}
	entity := o.Entity
	if entity == "" {
		for _, part := range strings.Split(name, "-") {
			if part != "" {
				entity += strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}
	if !entityName.MatchString(entity) || entity == "Problem" {
		return config.Resource{}, nil, fmt.Errorf("el nombre de entidad %q no sirve: usa --entity con un nombre que empiece en mayúscula (por ejemplo Afiliado)", entity)
	}
	r := config.Resource{Name: name, Entity: entity, Source: o.Source, Many: o.Many}
	if o.Subject != "" {
		r.DataSubject = &config.SubjectSpec{DocumentType: o.Subject}
	}
	var skipped []string
	for _, c := range obj.Columns {
		switch {
		case !plainColumn.MatchString(c.Name):
			skipped = append(skipped, fmt.Sprintf("%s: el nombre no es simple; renómbrala en la vista", c.Name))
		case c.Kind == KindUnsupported:
			skipped = append(skipped, fmt.Sprintf("%s: tipo %s, que no se puede exponer", strings.ToLower(c.Name), c.Native))
		default:
			r.Columns = append(r.Columns, config.ColumnSpec{Name: strings.ToLower(c.Name)})
		}
	}
	key, err := findColumn(obj, o.Key)
	if err != nil {
		return config.Resource{}, nil, err
	}
	if key.Kind == KindUnsupported || !plainColumn.MatchString(key.Name) {
		return config.Resource{}, nil, fmt.Errorf("la columna %q (%s) no se puede usar como clave", o.Key, key.Native)
	}
	r.Key.Column = strings.ToLower(key.Name)
	if len(r.Columns) == 0 {
		return config.Resource{}, nil, fmt.Errorf("%s no tiene columnas que se puedan exponer", o.Source)
	}
	return r, skipped, nil
}
