package lib_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// TestEveryIntrinsicIsDeclared pins the registry against the declarations, so
// the two cannot drift while both exist. What the compiler knows about an
// intrinsic is meant to be readable at the declaration; a registry entry with
// no declaration would be knowledge with nowhere to read it, and a mark whose
// id no registry answers to is a typo nothing else would catch.
//
// The id is the declaration's own SNGL name — `string.length`, not StrLength —
// so this also pins that convention.
func TestEveryIntrinsicIsDeclared(t *testing.T) {
	var registry []ir.IntrinsicDef
	for _, defs := range [][]ir.IntrinsicDef{
		ir.Intrinsics, ir.AlertIntrinsics, ir.FileIntrinsics,
		ir.I18nIntrinsics, ir.CanvasIntrinsics,
	} {
		registry = append(registry, defs...)
	}

	marked := map[string]bool{}
	err := fs.WalkDir(lib.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sngl") {
			return err
		}
		src, err := lib.FS.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range markRE.FindAllStringSubmatch(string(src), -1) {
			marked[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) == 0 {
		t.Fatal("no #[intrinsic] marks found in lib/")
	}

	inRegistry := map[string]bool{}
	for _, def := range registry {
		inRegistry[def.Name] = true
		if !marked[def.Name] {
			t.Errorf("registry has %q with no declaration marked #[intrinsic(%q)]", def.Name, def.Name)
		}
	}
	for id := range marked {
		if !inRegistry[id] {
			t.Errorf("declaration marked #[intrinsic(%q)] answers to no registry entry", id)
		}
	}
}

var markRE = regexp.MustCompile(`#\[intrinsic\("([^"]+)"`)

// A component a program can write is a component someone has to look up, so
// every exported one carries a doc comment. The draw shapes shipped without
// them and nothing noticed: `sngl doc` rendered a bare name and the website
// rendered an empty card.
func TestExportedComponentsAreDocumented(t *testing.T) {
	reg, _, err := checker.LoadStdlib()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg) == 0 {
		t.Fatal("no components in the stdlib registry")
	}
	for name, schema := range reg {
		if schema.Doc == "" {
			t.Errorf("component %s has no doc comment", name)
		}
	}
}
