package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/lib"
)

// TestCanvasKotlinDeclsInSync guards against drift between the hand-written
// Kotlin data class decls in canvasKotlinDecls and the source-of-truth SNGL
// struct declarations in lib/canvas.sngl (CanvasStyle, PathCmd) and
// lib/types.sngl (color → Color). These Kotlin decls are hand-written because
// the canvas stdlib structs aren't carried on pkg.Structs for the Kotlin path
// (mirrors what canvasutil_sync_test.go does for the Go-emitting platforms).
//
// android emits each SNGL field name verbatim as the Kotlin property name
// (identity mapping — e.g. `fill`, `strokeWidth`, `cx1`), unlike the Go path
// which capitalizes. It checks ALL fields, not just ones a fixture exercises:
// if a field is added/renamed in the .sngl but not in canvasKotlinDecls, this
// fails.
func TestCanvasKotlinDeclsInSync(t *testing.T) {
	// SNGL struct name → lib source file. The Kotlin decl name is the
	// SNGL name except `color` → `Color` (capitalized data class).
	want := map[string]string{
		"CanvasStyle": "canvas.sngl",
		"PathCmd":     "canvas.sngl",
		"color":       "types.sngl",
	}
	// SNGL struct name → Kotlin data class name as emitted by canvasKotlinDecls.
	kotlinName := map[string]string{
		"CanvasStyle": "CanvasStyle",
		"PathCmd":     "PathCmd",
		"color":       "Color",
	}

	files := map[string]struct{}{}
	for _, file := range want {
		files[file] = struct{}{}
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

	// Guard: every tracked struct must have been found with fields, otherwise
	// the test can silently pass by parsing nothing (e.g. file renamed/moved).
	for name := range want {
		if len(fieldsByStruct[name]) == 0 {
			t.Fatalf("struct %q not found (or has no fields) in lib stdlib sources; "+
				"the test can no longer verify drift", name)
		}
	}

	decls := canvasKotlinDecls(nil)

	// Split the emitted Kotlin into per-data-class bodies so a field present in
	// one decl can't satisfy a check for another struct.
	bodyOf := func(kName string) (string, bool) {
		marker := "data class " + kName + "("
		start := strings.Index(decls, marker)
		if start < 0 {
			return "", false
		}
		rest := decls[start+len(marker):]
		// canvasKotlinDecls closes each data class with a ")" at the start of
		// its own line ("\n)"). Field defaults may contain inline "(...)"
		// (e.g. CanvasStyle's `Color(a = 0)`), so match the line-leading close.
		end := strings.Index(rest, "\n)")
		if end < 0 {
			return rest, true
		}
		return rest[:end], true
	}

	for structName, fields := range fieldsByStruct {
		kName := kotlinName[structName]
		body, ok := bodyOf(kName)
		if !ok {
			t.Errorf("stdlib struct %s (Kotlin: %q) has no data class decl in canvasKotlinDecls",
				structName, kName)
			continue
		}
		for _, field := range fields {
			// canvasKotlinDecls emits "    var <name>: <type>" — match the
			// property by its "var <name>:" / "var <name> " form.
			if !strings.Contains(body, "var "+field+":") &&
				!strings.Contains(body, "var "+field+" ") {
				t.Errorf("stdlib struct %s field %q is declared in lib/%s "+
					"but missing as a property in canvasKotlinDecls data class %q — "+
					"update android canvas.go to match",
					structName, field, want[structName], kName)
			}
		}
	}
}
