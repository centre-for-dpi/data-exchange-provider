//go:build !canal

package instant

import (
	"regexp"
	"strings"

	"github.com/centre-for-dpi/data-exchange-provider/internal/config"
	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
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
		return config.Resource{}, nil, i18n.Errorf("the resource name %q does not work in a URL: use --resource with lower-case letters, digits and hyphens", name)
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
		return config.Resource{}, nil, i18n.Errorf("the entity name %q does not work: use --entity with a name that starts with an upper-case letter (for example Member)", entity)
	}
	r := config.Resource{Name: name, Entity: entity, Source: o.Source, Many: o.Many}
	if o.Subject != "" {
		r.DataSubject = &config.SubjectSpec{DocumentType: o.Subject}
	}
	var skipped []string
	lowered := map[string]int{}
	for _, c := range obj.Columns {
		lowered[strings.ToLower(c.Name)]++
	}
	for _, c := range obj.Columns {
		switch {
		case lowered[strings.ToLower(c.Name)] > 1:
			skipped = append(skipped, i18n.Sprintf("%s: another column differs only in case; rename it in the view", c.Name))
		case !plainColumn.MatchString(c.Name):
			skipped = append(skipped, i18n.Sprintf("%s: the name is not plain; rename it in the view", c.Name))
		case c.Kind == KindUnsupported:
			skipped = append(skipped, i18n.Sprintf("%s: type %s, which cannot be exposed", strings.ToLower(c.Name), c.Native))
		default:
			r.Columns = append(r.Columns, config.ColumnSpec{Name: strings.ToLower(c.Name)})
		}
	}
	key, err := findColumn(obj, o.Key)
	if err != nil {
		return config.Resource{}, nil, err
	}
	if key.Kind == KindUnsupported || !plainColumn.MatchString(key.Name) {
		return config.Resource{}, nil, i18n.Errorf("the column %q (%s) cannot be used as a key", o.Key, key.Native)
	}
	r.Key.Column = strings.ToLower(key.Name)
	if len(r.Columns) == 0 {
		return config.Resource{}, nil, i18n.Errorf("%s has no columns that can be exposed", o.Source)
	}
	return r, skipped, nil
}
