package lib_test

import (
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

// TestEveryPlatformAnswersEveryStyleField holds each platform that draws to
// every field of ui.Style: the fields its SNGL reads -- its style functions
// and its overrides -- together with the ones its `unstyled` const lists, with
// a reason, as fields it cannot express. A field added to ui.Style fails every
// platform until it reads the field or says why not, and a listed field the
// platform reads is a reason that is no longer true.
//
// A platform whose node names no generator draws nothing, so there is no
// field it could express: `none`, whose programs the interpreter runs, renders
// a tree a test reads and translates no Style.
func TestEveryPlatformAnswersEveryStyleField(t *testing.T) {
	loadEveryPackage(t)
	style := styleDef(t)
	var fields []string
	for _, f := range style.Fields {
		fields = append(fields, f.Name)
	}

	checked := 0
	for _, path := range lib.Packages() {
		name, ok := strings.CutPrefix(path, "platform/")
		if !ok || strings.Contains(name, "/") {
			continue
		}
		if _, inert := codegen.LookupPlatform(name).(*codegen.DeclaredPlatform); inert {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			pkg := checker.LibPackage(path)
			read := styleReads(pkg, name, style)
			listed := unstyled(t, pkg)
			for _, f := range fields {
				switch {
				case read[f] && listed[f]:
					t.Errorf("%s reads ui.Style.%s and lists it in unstyled", name, f)
				case !read[f] && !listed[f]:
					t.Errorf("%s neither reads ui.Style.%s nor lists it in unstyled with a reason", name, f)
				}
			}
			for f := range listed {
				if !slices.Contains(fields, f) {
					t.Errorf("%s lists %q in unstyled, which is no field of ui.Style", name, f)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no platform that draws was found")
	}
}

func styleDef(t *testing.T) *ir.StructDef {
	t.Helper()
	for _, s := range checker.LibPackage("ui").Structs {
		if s.Name == "Style" {
			return s
		}
	}
	t.Fatal("sngl:ui declares no Style")
	return nil
}

// styleReads is the set of ui.Style fields a platform's package reads: in
// its own declarations and in the overrides it writes of other packages'.
// A Style is matched by its package's path and its name, a named type's
// identity, rather than by pointer: the sngl:ui a target package's check
// read is not the one LibPackage hands back.
func styleReads(pkg *ir.Package, platform string, style *ir.StructDef) map[string]bool {
	read := map[string]bool{}
	visit := func(e ir.Expr) error {
		if s, ok := e.(*ir.Select); ok && s.Operand != nil {
			if sd, ok := s.Operand.ExprType().Decl.(*ir.StructDef); ok && sd.Name == style.Name && sd.PkgPath() == style.PkgPath() {
				read[s.Field] = true
			}
		}
		return nil
	}
	ir.WalkExprs(pkg, visit)
	for _, o := range pkg.Targets.Overrides {
		body, ok := o.Platform[platform]
		if !ok {
			continue
		}
		for _, v := range body.Vars {
			ir.WalkExprs(v.Init, visit)
		}
		ir.WalkExprs(body.Stmts, visit)
		for _, f := range body.Funcs {
			ir.WalkExprs(f, visit)
		}
	}
	return read
}

// unstyled is the platform's list of the ui.Style fields it cannot express.
// Saying nothing is not saying none: a platform answering every field writes
// an empty list.
func unstyled(t *testing.T, pkg *ir.Package) map[string]bool {
	t.Helper()
	for _, c := range pkg.Consts {
		if c.Name != "unstyled" {
			continue
		}
		list, ok := c.Init.(*ir.ListLit)
		if !ok {
			t.Fatalf("%s: unstyled is %T, not a list literal of field names", pkg.Path, c.Init)
		}
		listed := map[string]bool{}
		for _, e := range list.Elems {
			lit, ok := e.(*ir.Literal)
			if !ok {
				t.Fatalf("%s: unstyled holds %T, not a field name", pkg.Path, e)
			}
			listed[lit.Value] = true
		}
		return listed
	}
	t.Fatalf("%s declares no `const unstyled list[string]` of the ui.Style fields it cannot express", pkg.Path)
	return nil
}
