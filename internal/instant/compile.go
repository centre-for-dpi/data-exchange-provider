//go:build !canal

package instant

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
)

// Compiled is a resource checked against the catalog, ready to serve.
type Compiled struct {
	config.Resource          // Key.Pattern holds the effective, anchored pattern
	Query           string   // identifiers from the catalog, quoted; the key is the only parameter
	Columns         []Column // catalog columns, in declaration order
	Exposed         []Kind   // the kind each column is served as (after `as`)
	KeyColumn       Column
	KeyRegexp       *regexp.Regexp
	Object          Object // the catalog object, as the catalog spells it (without columns)
	Probe           string // SELECT of the columns that returns no rows: checks the user may read them
	Limit           int    // rows read: 2 for one row (a second one means the key is not unique), maxRows+1 for many
}

// defaultStringKeyLength bounds the default pattern of a text key without a declared length.
const defaultStringKeyLength = 64

// Compile checks every resource of the service against the catalog and reports every problem at
// once, in the operator's language (RID_LANG).
func Compile(svc config.Service, catalog []Object, d Dialect) ([]Compiled, error) {
	var out []Compiled
	var errs []error
	for _, r := range svc.Instant.Resources {
		c, rerrs := compileResource(r, catalog, d)
		for _, err := range rerrs {
			errs = append(errs, i18n.Errorf("resource %q: %w", r.Name, err))
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
	c := Compiled{Resource: r, Object: Object{Schema: obj.Schema, Name: obj.Name, Kind: obj.Kind}}
	key, err := findColumn(obj, r.Key.Column)
	if err != nil {
		errs = append(errs, i18n.Errorf("the key: %w", err))
	} else {
		c.KeyColumn = key
		pattern, err := keyPattern(key, r.Key.Pattern)
		if err != nil {
			errs = append(errs, err)
		} else if re, err := regexp.Compile(pattern); err != nil {
			errs = append(errs, i18n.Errorf("the key's pattern %q is not valid: %v", pattern, err))
		} else {
			c.Key.Pattern = pattern
			c.KeyRegexp = re
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
			errs = append(errs, i18n.Errorf("the column %q is of type %s, which the kit cannot expose: convert it in the view (for example to text or to a number) or remove it from columns", spec.Name, col.Native))
			continue
		}
		k, ok := exposedKind(col, spec.As)
		if !ok {
			errs = append(errs, i18n.Errorf("the column %q is of type %s and cannot be exposed with as: %s; with this type you can use %s",
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
	var orderBy []string
	if r.Many {
		orderBy = selected
	}
	c.Probe = "SELECT " + strings.Join(selected, ", ") + " FROM " + d.Quote(obj.Schema) + "." + d.Quote(obj.Name) + " WHERE 1 = 0"
	c.Query = d.SelectByKey(selected, d.Quote(obj.Schema)+"."+d.Quote(obj.Name), key, orderBy, c.Limit)
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
		return nil, i18n.Errorf("the database user cannot read %s (it is not among the views and tables with the SELECT permission)", source)
	}
	return nil, i18n.Errorf("%s is ambiguous: several objects differ only in case; write it exactly as in the database", source)
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
		return Column{}, i18n.Errorf("the column %q does not exist in %s.%s", name, obj.Schema, obj.Name)
	}
	return Column{}, i18n.Errorf("the column %q is ambiguous in %s.%s (several differ only in case)", name, obj.Schema, obj.Name)
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
		n = min(n, maxKeyBytes) // regexp repeat counts stop at 1000; keys never exceed maxKeyBytes
		return fmt.Sprintf(`^[A-Za-z0-9-]{1,%d}$`, n), nil
	case KindInteger:
		return `^[0-9]{1,38}$`, nil // beyond int64, decimal columns get the key as text
	case KindNumber:
		return `^[0-9]{1,38}$`, nil
	case KindUUID:
		return `^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`, nil
	case KindDate:
		return `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`, nil
	case KindUnsupported:
		return "", i18n.Errorf("the key %q is of type %s, which cannot be used as a key", key.Name, key.Native)
	}
	return "", i18n.Errorf("the key %q is of type %s: declare key.pattern with the format it accepts", key.Name, key.Native)
}
