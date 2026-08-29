package canvasutil

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/lib"
)

// TestStructDeclsInSync guards against drift between the hardcoded Go struct
// decls in StructDecls and the source-of-truth SNGL struct declarations in
// lib/canvas.sngl (CanvasStyle, PathCmd). These Go decls are hand-written
// because those canvas stdlib structs aren't carried on the Go codegen path;
// every Go-emitting canvas platform (fyne, gtk4) consumes StructDecls, so this
// single test covers them all.
//
// (color lives in lib/types.sngl but maps to the shared pkg/go/snglcolor.Color
// type rather than a StructDecls entry — see TestColorFieldsInSync.)
//
// It checks ALL fields, not just ones a fixture happens to exercise. If a
// field is added/renamed in the .sngl but not in StructDecls, this fails.
func TestStructDeclsInSync(t *testing.T) {
	want := map[string]string{
		"CanvasStyle": "ui/draw/draw.sngl",
		"PathCmd":     "ui/draw/draw.sngl",
	}

	files := map[string][]string{}
	for name, file := range want {
		files[file] = append(files[file], name)
	}

	fieldsByStruct := map[string][]string{}
	for file := range files {
		src, err := lib.FS.ReadFile(file)
		if err != nil {
			t.Fatalf("read lib/%s: %v", file, err)
		}
		doc, err := parser.Parse(file, src)
		if err != nil {
			t.Fatalf("parse lib/%s: %v", file, err)
		}
		for _, stmt := range doc.Stmts {
			sd, ok := structDefOf(stmt)
			if !ok {
				continue
			}
			if _, tracked := want[sd.Name]; !tracked {
				continue
			}
			for _, f := range sd.Fields() {
				fieldsByStruct[sd.Name] = append(fieldsByStruct[sd.Name], f.Names...)
			}
		}
	}

	for name := range want {
		if len(fieldsByStruct[name]) == 0 {
			t.Fatalf("struct %q not found (or has no fields) in lib stdlib sources; "+
				"the test can no longer verify drift", name)
		}
	}

	for structName, fields := range fieldsByStruct {
		goStruct := golang.ExportName(structName)
		decl, ok := StructDecls[goStruct]
		if !ok {
			t.Errorf("stdlib struct %s (Go: %q) has no hardcoded decl in StructDecls",
				structName, goStruct)
			continue
		}
		for _, field := range fields {
			goField := golang.ExportName(field)
			if !strings.Contains(decl, "\t"+goField+" ") && !strings.Contains(decl, "\t"+goField+"\t") {
				t.Errorf("stdlib struct %s field %q (Go: %q) is declared in lib/*.sngl "+
					"but missing from StructDecls[%q] — update canvasutil to match",
					structName, field, goField, goStruct)
			}
		}
	}
}

// TestColorFieldsInSync guards that lib/types.sngl's color struct still has
// exactly the r,g,b,a channels that pkg/go/snglcolor.Color (referenced by
// ColorGoType) mirrors as R,G,B,A. If the SNGL color gains/loses a channel,
// the shared Go runtime type must be updated to match.
func TestColorFieldsInSync(t *testing.T) {
	src, err := lib.FS.ReadFile("builtin/color.sngl")
	if err != nil {
		t.Fatalf("read lib/types.sngl: %v", err)
	}
	doc, err := parser.Parse("builtin/color.sngl", src)
	if err != nil {
		t.Fatalf("parse lib/types.sngl: %v", err)
	}
	var got []string
	for _, stmt := range doc.Stmts {
		sd, ok := structDefOf(stmt)
		if !ok || sd.Name != "color" {
			continue
		}
		for _, f := range sd.Fields() {
			got = append(got, f.Names...)
		}
	}
	want := []string{"r", "g", "b", "a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("lib color fields = %v, want %v — update pkg/go/snglcolor.Color (ColorGoType %q) to match",
			got, want, ColorGoType)
	}
}

// structDefOf returns the *ast.StructDef stmt is, marks and all.
func structDefOf(stmt ast.Stmt) (*ast.StructDef, bool) {
	sd, ok := stmt.(*ast.StructDef)
	return sd, ok
}
