package fyne

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/lib"
)

// TestCanvasStdlibDeclsInSync guards against drift between the hardcoded Go
// struct decls in canvasStructDecls and the source-of-truth SNGL struct
// declarations in lib/canvas.sngl (CanvasStyle, PathCmd) and lib/types.sngl
// (color → Color). The Go decls are hand-written because these stdlib structs
// aren't carried on the Go codegen path; this test ensures every stdlib field
// has a corresponding exported Go field in the emitted decls.
//
// It checks ALL fields, not just ones a fixture happens to exercise. If a
// field is added/renamed in the .sngl but not in canvasStructDecls, this fails.
func TestCanvasStdlibDeclsInSync(t *testing.T) {
	want := map[string]string{
		"CanvasStyle": "canvas.sngl",
		"PathCmd":     "canvas.sngl",
		"color":       "types.sngl",
	}

	files := map[string][]string{} // filename → struct names sourced from it
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
			sd, ok := stmt.(*ast.StructDef)
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
		decl, ok := canvasStructDecls[goStruct]
		if !ok {
			t.Errorf("stdlib struct %s (Go: %q) has no hardcoded decl in canvasStructDecls",
				structName, goStruct)
			continue
		}
		for _, field := range fields {
			goField := golang.ExportName(field)
			// Match the field as a Go struct field: `<Name>` followed by
			// whitespace (the field type). Anchored on a tab to avoid matching
			// substrings of longer field names.
			if !strings.Contains(decl, "\t"+goField+" ") && !strings.Contains(decl, "\t"+goField+"\t") {
				t.Errorf("stdlib struct %s field %q (Go: %q) is declared in lib/*.sngl "+
					"but missing from canvasStructDecls[%q] — update canvas.go to match",
					structName, field, goField, goStruct)
			}
		}
	}
}
