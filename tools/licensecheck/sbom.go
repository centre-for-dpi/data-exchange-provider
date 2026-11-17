package main

import (
	"encoding/json"
	"sort"
)

// module is one Go module linked into the binary, with the license licensecheck found.
type module struct {
	Path, Version, License string
}

// spdxIDs are the licenses classify returns that are SPDX identifiers (an exception's reason is
// free text, so it goes as a license name).
var spdxIDs = map[string]bool{"MIT": true, "Apache-2.0": true, "BSD-2-Clause": true, "BSD-3-Clause": true,
	"ISC": true, "MPL-2.0": true, "0BSD": true, "Zlib": true, "Unlicense": true, "MIT-0": true,
	"EPL-2.0": true, "BlueOak-1.0.0": true, "Python-2.0": true}

type cdxLicense struct {
	License struct {
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
	} `json:"license"`
}

type cdxComponent struct {
	Type     string       `json:"type"`
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	Purl     string       `json:"purl,omitempty"`
	Licenses []cdxLicense `json:"licenses,omitempty"`
}

// cyclonedx is a CycloneDX 1.5 SBOM of the binary: deterministic (components sorted, no
// timestamp or serial number), so the same build gives the same file.
func cyclonedx(name, version, variant string, mods []module) ([]byte, error) {
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	components := make([]cdxComponent, 0, len(mods))
	for _, m := range mods {
		var l cdxLicense
		if spdxIDs[m.License] {
			l.License.ID = m.License
		} else {
			l.License.Name = m.License
		}
		components = append(components, cdxComponent{Type: "library", Name: m.Path, Version: m.Version,
			Purl: "pkg:golang/" + m.Path + "@" + m.Version, Licenses: []cdxLicense{l}})
	}
	doc := map[string]any{
		"bomFormat":   "CycloneDX",
		"specVersion": "1.5",
		"version":     1,
		"metadata": map[string]any{
			"component":  map[string]any{"type": "application", "name": name, "version": version},
			"properties": []map[string]string{{"name": "den:variant", "value": variant}},
		},
		"components": components,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n'), err
}
