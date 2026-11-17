//go:build !canal

package instant

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
)

// Compiled is a resource checked against the catalog, ready to serve.
type Compiled struct {
	config.Resource          // Key.Pattern holds the effective, anchored pattern
	Query           string   // identifiers from the catalog, quoted; the key is the only parameter
	Columns         []Column // catalog columns, in declaration order
	Exposed         []Kind   // the kind each column is served as (after `as`)
	KeyColumn       Column
	KeyRegexp       *regexp.Regexp
	Limit           int // rows read: 2 for one row (a second one means the key is not unique), maxRows+1 for many
}

// defaultStringKeyLength bounds the default pattern of a text key without a declared length.
const defaultStringKeyLength = 64

// Compile checks every resource of the service against the catalog and reports every problem at
// once, in Spanish.
func Compile(svc config.Service, catalog []Object, d Dialect) ([]Compiled, error) {
	var out []Compiled
	var errs []error
	for _, r := range svc.Instant.Resources {
		c, rerrs := compileResource(r, catalog, d)
		for _, err := range rerrs {
			errs = append(errs, fmt.Errorf("recurso %q: %w", r.Name, err))
		}
		if len(rerrs) == 0 {
			out = append(out, c)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func compileResource(r config.Resource, catalog []Object, d Dialect) (Compiled, []error) {
	var errs []error
	obj, err := findObject(catalog, r.Source)
	if err != nil {
		return Compiled{}, []error{err}
	}
	c := Compiled{Resource: r}
	key, err := findColumn(obj, r.Key.Column)
	if err != nil {
		errs = append(errs, fmt.Errorf("la clave: %w", err))
	} else {
		c.KeyColumn = key
		pattern, err := keyPattern(key, r.Key.Pattern)
		if err != nil {
			errs = append(errs, err)
		} else {
			c.Key.Pattern = pattern
			c.KeyRegexp = regexp.MustCompile(pattern)
		}
	}
	var selected []string
	for _, spec := range r.Columns {
		col, err := findColumn(obj, spec.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if col.Kind == KindUnsupported {
			errs = append(errs, fmt.Errorf("la columna %q es de tipo %s, que el kit no puede exponer: conviértela en la vista (por ejemplo a texto o a número) o quítala de columns", spec.Name, col.Native))
			continue
		}
		k, ok := exposedKind(col, spec.As)
		if !ok {
			errs = append(errs, fmt.Errorf("la columna %q es de tipo %s y no se puede exponer con as: %s; con este tipo se puede usar %s",
				spec.Name, col.Native, spec.As, possibleAs(col.Kind)))
			continue
		}
		c.Columns = append(c.Columns, col)
		c.Exposed = append(c.Exposed, k)
		selected = append(selected, d.Quote(col.Name))
	}
	if len(errs) > 0 {
		return Compiled{}, errs
	}
	c.Limit = 2
	if r.Many {
		c.Limit = r.MaxRows + 1
	}
	c.Query = d.SelectByKey(selected, d.Quote(obj.Schema)+"."+d.Quote(obj.Name), d.Quote(key.Name), c.Limit)
	return c, nil
}

// findObject matches schema.name exactly first, then without case (Oracle stores upper case).
func findObject(catalog []Object, source string) (*Object, error) {
	var folded []*Object
	for i := range catalog {
		full := catalog[i].Schema + "." + catalog[i].Name
		if full == source {
			return &catalog[i], nil
		}
		if strings.EqualFold(full, source) {
			folded = append(folded, &catalog[i])
		}
	}
	switch len(folded) {
	case 1:
		return folded[0], nil
	case 0:
		return nil, fmt.Errorf("el usuario de la base no puede leer %s (no está entre las vistas y tablas con permiso SELECT)", source)
	}
	return nil, fmt.Errorf("%s es ambiguo: hay varios objetos que solo cambian en mayúsculas; escríbelo exactamente como en la base", source)
}

func findColumn(obj *Object, name string) (Column, error) {
	var folded []Column
	for _, c := range obj.Columns {
		if c.Name == name {
			return c, nil
		}
		if strings.EqualFold(c.Name, name) {
			folded = append(folded, c)
		}
	}
	switch len(folded) {
	case 1:
		return folded[0], nil
	case 0:
		return Column{}, fmt.Errorf("la columna %q no existe en %s.%s", name, obj.Schema, obj.Name)
	}
	return Column{}, fmt.Errorf("la columna %q es ambigua en %s.%s (varias solo cambian en mayúsculas)", name, obj.Schema, obj.Name)
}

// keyPattern is the entity's pattern, anchored to the whole key, or a default by kind. Default
// patterns never admit spaces, quotes or separators.
func keyPattern(key Column, declared string) (string, error) {
	if declared != "" {
		return "^(?:" + declared + ")$", nil
	}
	switch key.Kind {
	case KindString:
		n := key.MaxLength
		if n <= 0 {
			n = defaultStringKeyLength
		}
		return fmt.Sprintf(`^[A-Za-z0-9-]{1,%d}$`, n), nil
	case KindInteger:
		return `^[0-9]{1,18}$`, nil
	case KindNumber:
		return `^[0-9]{1,38}$`, nil
	case KindUUID:
		return `^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`, nil
	case KindDate:
		return `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`, nil
	case KindUnsupported:
		return "", fmt.Errorf("la clave %q es de tipo %s, que no se puede usar como clave", key.Name, key.Native)
	}
	return "", fmt.Errorf("la clave %q es de tipo %s: declara key.pattern con el formato que acepta", key.Name, key.Native)
}
