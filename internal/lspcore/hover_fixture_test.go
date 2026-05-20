package lspcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestHoverFixtures(t *testing.T) {
	matches, err := filepath.Glob("../../testdata/lsp_hover/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no hover fixtures found")
	}
	for _, path := range matches {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, perr := parser.Parse(filepath.Base(path), src)
			if perr != nil {
				t.Fatalf("parse %s: %v", path, perr)
			}
			dirs, err := testutil.ParseHoverDirectives(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(dirs) == 0 {
				t.Fatal("no HOVER directives in fixture")
			}
			fixtureDir := filepath.Dir(path)

			// Type-check the doc so prop / struct-field callbacks can
			// resolve against the real package (including stdlib).
			absDir, _ := filepath.Abs(fixtureDir)
			pkg, _ := sngl.Check(doc, absDir)

			opts := HoverOptions{
				ComponentImageURL: func(name string) (string, bool) {
					candidate := filepath.Join(fixtureDir, "snapshots", "example_"+name+"_html.png")
					info, err := os.Stat(candidate)
					if err != nil || info.IsDir() {
						return "", false
					}
					return "file://" + candidate, true
				},
				ComponentProp: func(componentName, propName string) (string, bool) {
					if pkg == nil {
						return "", false
					}
					comp := findFixtureComponent(pkg, componentName)
					if comp == nil {
						return "", false
					}
					for _, p := range comp.Props {
						if p.Name == propName {
							t := "dyn"
							if p.Type != nil {
								t = p.Type.String()
							}
							required := "required"
							if p.Default != nil {
								required = "optional"
							}
							return fmt.Sprintf("```sngl\n%s.%s: %s\n```\n\n%s prop on `component %s`.\n",
								comp.Name, p.Name, t, required, comp.Name), true
						}
					}
					return "", false
				},
				StructFieldType: func(structName, fieldName string) (string, bool) {
					if pkg == nil {
						return "", false
					}
					sd := findFixtureStruct(pkg, structName)
					if sd == nil {
						return "", false
					}
					for _, f := range sd.Fields {
						if f.Name == fieldName {
							t := "dyn"
							if f.Type != nil {
								t = f.Type.String()
							}
							return fmt.Sprintf("```sngl\n%s.%s: %s\n```\n\nfield on `struct %s`.\n",
								sd.Name, f.Name, t, sd.Name), true
						}
					}
					return "", false
				},
			}
			for _, d := range dirs {
				got := HoverAt(string(src), doc, d.Line, d.Col, opts)
				if d.Negate {
					if strings.Contains(got, d.Substring) {
						t.Errorf("HOVER-NOT(%s) %q matched at %d:%d (directive line %d)\n--- got ---\n%s",
							d.Target, d.Substring, d.Line, d.Col, d.DirLine, got)
					}
					continue
				}
				if !strings.Contains(got, d.Substring) {
					t.Errorf("HOVER(%s) missing %q at %d:%d (directive line %d)\n--- got ---\n%s",
						d.Target, d.Substring, d.Line, d.Col, d.DirLine, got)
				}
			}
		})
	}
}

func findFixtureComponent(pkg *ir.Package, name string) *ir.Component {
	if pkg == nil {
		return nil
	}
	if pkg.Symbols != nil {
		if sym, ok := pkg.Symbols.Comps[name]; ok {
			if c, ok := sym.(*ir.Component); ok {
				return c
			}
		}
	}
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	for _, imp := range pkg.Imports {
		if imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if c.Name == name {
				return c
			}
		}
	}
	return nil
}

func findFixtureStruct(pkg *ir.Package, name string) *ir.StructDef {
	if pkg == nil {
		return nil
	}
	if pkg.Symbols != nil {
		if sym, ok := pkg.Symbols.Types[name]; ok {
			if sd, ok := sym.(*ir.StructDef); ok {
				return sd
			}
		}
	}
	for _, sd := range pkg.Structs {
		if sd.Name == name {
			return sd
		}
	}
	for _, imp := range pkg.Imports {
		if imp.Pkg == nil {
			continue
		}
		for _, sd := range imp.Pkg.Structs {
			if sd.Name == name {
				return sd
			}
		}
	}
	return nil
}
