package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The functions whose first argument (or, for Fprintf, second) is a message of the catalog.
var messageFuncs = map[string]int{"T": 0, "Sprintf": 0, "Errorf": 0, "New": 0, "Fprintf": 1, "Fprintln": 1}

var verbRe = regexp.MustCompile(`%[-+# 0]*(\[\d+\])?(\d+|\*)?(\.(\d+|\*))?[a-zA-Z%]`)

func verbs(s string) []string { return verbRe.FindAllString(s, -1) }

// scanMessages walks the Go sources under root (tests excluded) and returns every message passed
// to this package, with where it is used. A message that is not a string constant cannot be
// checked against the catalog, so it is a problem.
func scanMessages(t *testing.T, root string) (map[string]string, []string) {
	t.Helper()
	used := map[string]string{}
	var problems []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "spikes", "test", "dist", "node_modules", ".git", "tools":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "internal/i18n/") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		name := importName(f)
		if name == "" {
			return nil
		}
		consts := stringConsts(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != name {
				return true
			}
			idx, ok := messageFuncs[sel.Sel.Name]
			if !ok || len(call.Args) <= idx {
				return true
			}
			where := fset.Position(call.Pos()).String()
			msg, ok := stringValue(call.Args[idx], consts)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: %s.%s needs a string constant as its message", where, name, sel.Sel.Name))
				return true
			}
			used[msg] = where
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return used, problems
}

func importName(f *ast.File) string {
	for _, imp := range f.Imports {
		if strings.HasSuffix(strings.Trim(imp.Path.Value, `"`), "/internal/i18n") {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "i18n"
		}
	}
	return ""
}

// stringConsts are the file's string constants, so a message can be a named constant.
func stringConsts(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i < len(vs.Values) {
					if v, ok := stringValue(vs.Values[i], nil); ok {
						out[n.Name] = v
					}
				}
			}
		}
	}
	return out
}

func stringValue(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		a, ok1 := stringValue(v.X, consts)
		b, ok2 := stringValue(v.Y, consts)
		return a + b, ok1 && ok2
	case *ast.ParenExpr:
		return stringValue(v.X, consts)
	}
	return "", false
}
