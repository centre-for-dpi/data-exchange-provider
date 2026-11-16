package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name, text, spdx string
		ok               bool
	}{
		{"mit", "Permission is hereby granted, free of charge, to any person obtaining a copy", "MIT", true},
		{"apache", "Apache License\n Version 2.0, January 2004\n http://www.apache.org/licenses/", "Apache-2.0", true},
		{"bsd3", "Redistribution and use in source and binary forms, with or without modification, are permitted... Neither the name of Google Inc. nor the names", "BSD-3-Clause", true},
		{"bsd2", "Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:", "BSD-2-Clause", true},
		{"mpl", "Mozilla Public License Version 2.0\n==================================", "MPL-2.0", true},
		{"isc", "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted", "ISC", true},
		{"lgpl", "GNU LESSER GENERAL PUBLIC LICENSE Version 3 ... the GNU General Public License", "LGPL", true},
		{"agpl", "GNU AFFERO GENERAL PUBLIC LICENSE Version 3, 19 November 2007", "AGPL", false},
		{"gpl", "GNU GENERAL PUBLIC LICENSE Version 2, June 1991", "GPL", false},
		{"mpl-mentions-gpl", "Mozilla Public License Version 2.0\n1.12. \"Secondary License\" means either the GNU General Public License, Version 2.0, the GNU Lesser General Public License, Version 2.1, the GNU Affero General Public License, Version 3.0", "MPL-2.0", true},
		{"epl-mentions-gpl", "Eclipse Public License - v 2.0 ... Secondary Licenses: GNU General Public License, Version 2.0", "EPL-2.0", true},
		{"unknown", "All rights reserved. You may not redistribute this software.", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spdx, ok := classify(c.text)
			if spdx != c.spdx || ok != c.ok {
				t.Fatalf("classify = (%q, %v), want (%q, %v)", spdx, ok, c.spdx, c.ok)
			}
		})
	}
}

func TestDualLicenseAllowedIfAnyFileAllowed(t *testing.T) {
	spdx, ok := judge("example.com/dual", []string{"GNU GENERAL PUBLIC LICENSE Version 2", "Permission is hereby granted, free of charge"}, nil)
	if !ok || spdx != "MIT" {
		t.Fatalf("judge = (%q, %v), want (MIT, true)", spdx, ok)
	}
}

func TestExceptionsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exceptions.json")
	if err := os.WriteFile(path, []byte(`{"example.com/odd": "PostgreSQL — OSI approved, reviewed 2026-11-16"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	exceptions, err := loadExceptions(path)
	if err != nil {
		t.Fatal(err)
	}
	spdx, ok := judge("example.com/odd", []string{"some license text the classifier does not know"}, exceptions)
	if !ok || spdx != "PostgreSQL — OSI approved, reviewed 2026-11-16" {
		t.Fatalf("judge = (%q, %v)", spdx, ok)
	}
}

func TestNoLicenseFileIsViolation(t *testing.T) {
	if _, ok := judge("example.com/none", nil, nil); ok {
		t.Fatal("a module without license files must not pass")
	}
}
