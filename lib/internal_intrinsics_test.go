package lib_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"

	// Registers the targets whose packages hold every component intrinsic:
	// a platform carries its own source now, so walking lib.FS alone finds
	// only the function half.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// TestEveryIntrinsicIsDeclared pins the registry against the declarations, so
// the two cannot drift while both exist. What the compiler knows about an
// intrinsic is meant to be readable at the declaration; a registry entry with
// no declaration would be knowledge with nowhere to read it, and a mark whose
// id no registry answers to is a typo nothing else would catch.
//
// The id is the declaration's own SNGL name — `string.length`, not StrLength —
// so this also pins that convention.
//
// A marked component is held to the opposite rule. The registry is signatures
// every language backend must implement; a component intrinsic is a widget one
// platform's codegen emits, and its props and events are the declaration
// itself. An entry here would be a signature nobody reads. Its id carries the
// emitting platform as a namespace, which is what keeps the two id spaces from
// ever meeting.
func TestEveryIntrinsicIsDeclared(t *testing.T) {
	var registry []ir.IntrinsicDef
	for _, defs := range [][]ir.IntrinsicDef{
		ir.Intrinsics, ir.AlertIntrinsics, ir.FileIntrinsics,
		ir.I18nIntrinsics, ir.CanvasIntrinsics, ir.RemoteIntrinsics,
	} {
		registry = append(registry, defs...)
	}

	marked := map[string]bool{}
	markedComponents := map[string]bool{}
	scan := func(src []byte) {
		for _, m := range markRE.FindAllStringSubmatch(string(src), -1) {
			if m[2] == "component" {
				markedComponents[m[1]] = true
				continue
			}
			marked[m[1]] = true
		}
	}
	walk := func(fsys fs.FS) error {
		return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sngl") {
				return err
			}
			src, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			scan(src)
			return nil
		})
	}
	if err := walk(lib.FS); err != nil {
		t.Fatal(err)
	}
	// Every source the compiler reads, not just the embedded library: a target
	// declares its own primitives, and those are the component intrinsics.
	for _, target := range targetsWithPackages() {
		fsys, ok := target.(interface{ PackageFS() fs.FS })
		if !ok || fsys.PackageFS() == nil {
			continue
		}
		if err := walk(fsys.PackageFS()); err != nil {
			t.Fatal(err)
		}
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
	if len(markedComponents) == 0 {
		t.Error("no component carries #[intrinsic]; the form is unexercised")
	}
	for id := range markedComponents {
		if inRegistry[id] {
			t.Errorf("component marked #[intrinsic(%q)] also has a registry entry; a component has no signature to register", id)
		}
		ns, name, ok := strings.Cut(id, ":")
		if !ok || ns == "" || name == "" {
			t.Errorf("component marked #[intrinsic(%q)] is not namespaced; want \"<platform>:<Name>\"", id)
		}
	}
}

// markRE captures an #[intrinsic] id and the declaration form it was written
// on. The declaration may carry a doc comment between the two.
var markRE = regexp.MustCompile(`#\[intrinsic\("([^"]+)"[^\]]*\]\s*(?://[^\n]*\n\s*)*(func|component)\b`)

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

// targetsWithPackages is every registered platform and language, which is where
// a target's own library package lives.
func targetsWithPackages() []any {
	var out []any
	for _, p := range codegen.CollectPlatforms() {
		out = append(out, p)
	}
	for _, name := range codegen.Langs() {
		out = append(out, codegen.LookupLang(name))
	}
	return out
}
