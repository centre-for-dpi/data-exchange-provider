package proxy

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type template struct {
	path   string
	re     *regexp.Regexp
	params int
}

type templates []template

// loadTemplates reads the paths of an OpenAPI document (JSON or YAML) so the access log can
// name the operation's template instead of the real path. Literal paths win over parameters.
func loadTemplates(file string) (templates, error) {
	if file == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("openapi %s: %w", file, err)
	}
	var doc struct {
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapi %s: %w", file, err)
	}
	var ts templates
	param := regexp.MustCompile(`\{[^/{}]+\}`)
	for p := range doc.Paths {
		parts := param.Split(p, -1)
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		ts = append(ts, template{path: p, re: regexp.MustCompile("^" + strings.Join(parts, "[^/]+") + "/?$"),
			params: len(param.FindAllString(p, -1))})
	}
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].params != ts[j].params {
			return ts[i].params < ts[j].params
		}
		return len(ts[i].path) > len(ts[j].path)
	})
	return ts, nil
}

func (ts templates) match(path string) string {
	for _, t := range ts {
		if t.re.MatchString(path) {
			return t.path
		}
	}
	return ""
}
