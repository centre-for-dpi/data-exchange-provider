package main

import "strings"

// rule is one license recognized by a distinctive phrase of its text (lower-cased, spaces
// collapsed). Order matters: MPL-2.0 and EPL-2.0 name the GPL family as "secondary licenses",
// so they come first; AGPL and LGPL texts mention the GPL, so they come before it.
type rule struct {
	spdx    string
	allowed bool
	all     []string // every phrase must appear
	none    []string // no phrase may appear
}

// Allowed list: rule 1 of CLAUDE.md (OSI approved; GPL/AGPL only as separate services, never
// linked into the kit's binary).
var rules = []rule{
	{spdx: "MPL-2.0", allowed: true, all: []string{"mozilla public license", "2.0"}},
	{spdx: "EPL-2.0", allowed: true, all: []string{"eclipse public license", "2.0"}},
	{spdx: "AGPL", allowed: false, all: []string{"gnu affero general public license"}},
	{spdx: "LGPL", allowed: true, all: []string{"gnu lesser general public license"}},
	{spdx: "GPL", allowed: false, all: []string{"gnu general public license"}},
	{spdx: "Apache-2.0", allowed: true, all: []string{"apache license", "version 2.0"}},
	{spdx: "BSD-3-Clause", allowed: true, all: []string{"redistribution and use in source and binary forms", "neither the name"}},
	{spdx: "BSD-2-Clause", allowed: true, all: []string{"redistribution and use in source and binary forms"}},
	{spdx: "MIT", allowed: true, all: []string{"permission is hereby granted, free of charge"}},
	{spdx: "ISC", allowed: true, all: []string{"permission to use, copy, modify, and/or distribute this software for any purpose"}},
	{spdx: "Zlib", allowed: true, all: []string{"altered source versions must be plainly marked as such"}},
	{spdx: "Unlicense", allowed: true, all: []string{"this is free and unencumbered software released into the public domain"}},
	{spdx: "BlueOak-1.0.0", allowed: true, all: []string{"blue oak model license"}},
}

func normalize(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// classify returns the SPDX-like name of a license text and whether rule 1 allows it.
func classify(text string) (string, bool) {
	t := normalize(text)
next:
	for _, r := range rules {
		for _, p := range r.all {
			if !strings.Contains(t, p) {
				continue next
			}
		}
		for _, p := range r.none {
			if strings.Contains(t, p) {
				continue next
			}
		}
		return r.spdx, r.allowed
	}
	return "", false
}
