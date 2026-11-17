// Command licensecheck fails when a Go module linked into the given packages has a license
// outside the kit's allowed list (CLAUDE.md, rule 1).
//
//	go run ./tools/licensecheck [-tags canal] [-cyclonedx sbom.json -version 0.1.0] ./cmd/den-node
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const mainModule = "github.com/centre-for-dpi/data-exchange-provider"

func main() {
	tags := flag.String("tags", "", "build tags")
	exceptionsFile := flag.String("exceptions", "tools/licensecheck/exceptions.json", "reviewed exceptions: module → reason")
	sbomFile := flag.String("cyclonedx", "", "also write a CycloneDX SBOM of the linked modules to this file")
	version := flag.String("version", "dev", "the kit's version, for the SBOM")
	flag.Parse()
	exceptions, err := loadExceptions(*exceptionsFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "licensecheck:", err)
		os.Exit(2)
	}
	args := []string{"list", "-deps", "-f", "{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}"}
	if *tags != "" {
		args = append(args, "-tags", *tags)
	}
	out, err := exec.Command("go", append(args, flag.Args()...)...).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "licensecheck: go list:", err)
		os.Exit(2)
	}
	seen := map[string]bool{}
	var lines []string
	var mods []module
	failed := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[0] == mainModule || seen[parts[0]] {
			continue
		}
		seen[parts[0]] = true
		spdx, ok := judge(parts[0], licenseTexts(parts[2]), exceptions)
		mark := "ok"
		if !ok {
			mark, failed = "NOT ALLOWED", true
		}
		lines = append(lines, fmt.Sprintf("%-12s %-55s %s %s", mark, parts[0], parts[1], spdx))
		mods = append(mods, module{Path: parts[0], Version: parts[1], License: spdx})
	}
	sort.Strings(lines)
	fmt.Println(strings.Join(lines, "\n"))
	if failed {
		os.Exit(1)
	}
	if *sbomFile != "" {
		variant := "completo"
		if strings.Contains(*tags, "canal") {
			variant = "canal"
		}
		b, err := cyclonedx("den-node", *version, variant, mods)
		if err == nil {
			err = os.WriteFile(*sbomFile, b, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "licensecheck: sbom:", err)
			os.Exit(2)
		}
	}
}

// licenseTexts reads every LICENSE*, LICENCE* and COPYING* file at the module's root.
func licenseTexts(dir string) []string {
	var texts []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := strings.ToUpper(e.Name())
		if e.IsDir() || !(strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "LICENCE") || strings.HasPrefix(name, "COPYING")) {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			texts = append(texts, string(b))
		}
	}
	return texts
}

// judge decides a module: a reviewed exception wins; otherwise every license file must be
// allowed and none may contain a prohibited license or rider. No license file fails.
func judge(module string, texts []string, exceptions map[string]string) (string, bool) {
	if reason, ok := exceptions[module]; ok {
		return reason, true
	}
	if len(texts) == 0 {
		return "no license file", false
	}
	for _, t := range texts {
		if d := denied(t); d != "" {
			return "prohibited: " + d, false
		}
	}
	var allowed, refused []string
	for _, t := range texts {
		spdx, ok := classify(t)
		if spdx == "" {
			spdx = "unknown"
		}
		if ok {
			allowed = append(allowed, spdx)
		} else {
			refused = append(refused, spdx)
		}
	}
	if len(refused) > 0 { // every license file must be allowed; a reviewed exception can override
		return strings.Join(append(allowed, refused...), ", "), false
	}
	return allowed[0], true
}

func loadExceptions(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	return m, json.Unmarshal(b, &m)
}
