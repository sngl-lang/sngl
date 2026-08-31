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

// TestEveryIntrinsicIsDeclared pins the registry against the declarations. It
// used to hold a hand-written table in ir to them; the registry is built from
// the declarations now, so what it still says is that loading every package
// registers every marked id — an intrinsic is registered as its declaration
// is, and a target's arrive with its package rather than with lib/.
//
// A marked component is held to the opposite rule. The registry is signatures
// every language backend must implement; a component intrinsic is a widget one
// platform's codegen emits, and its props and events are the declaration
// itself. An entry here would be a signature nobody reads. Its id carries the
// emitting platform as a namespace, which is what keeps the two id spaces from
// ever meeting.
func TestEveryIntrinsicIsDeclared(t *testing.T) {
	loadEveryPackage(t)
	registry := ir.AllIntrinsics()

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
// loadEveryPackage checks every library and target package, which is what
// populates the intrinsic registry: an intrinsic is registered as its
// declaration is, so nothing is known about one whose package never loaded.
func loadEveryPackage(t *testing.T) {
	t.Helper()
	for _, name := range lib.Packages() {
		if checker.LibPackage(name) == nil {
			t.Errorf("library package %q did not load", name)
		}
	}
	for _, p := range codegen.CollectPlatforms() {
		fsys, ok := p.(interface{ PackageFS() fs.FS })
		if !ok || fsys.PackageFS() == nil {
			continue
		}
		checker.LibPackage("platform/" + p.PlatformIdentifier())
	}
	for _, name := range codegen.Langs() {
		checker.LibPackage("language/" + name)
	}
}

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

// TestEveryIntrinsicIsImplemented is the contract the registry was said to
// state and never checked. A declaration marked #[intrinsic] is a signature
// whose result comes from the target's implementation of the id, so an id
// nothing implements is a build emitting a call to a function that does not
// exist. A table in ir could only ever say the id was in a Go list.
//
// Three ways an id is legitimately absent from the language emitter registry,
// each read off the declaration rather than off a list of names:
//
//   - `usable` says the declaration's own SNGL body computes the same answer,
//     so a backend may emit the body instead.
//   - sngl:internal/draw's primitives take a platform draw context, so the
//     platform emits them through IntrinsicTranslator and no language does.
//   - error.raise lowers to each target's abort form rather than to a call at
//     all, which ir.IsErrorRaiseFunc is the compiler's own statement of.
func TestEveryIntrinsicIsImplemented(t *testing.T) {
	loadEveryPackage(t)
	langs := codegen.Langs()
	if len(langs) == 0 {
		t.Fatal("no languages registered")
	}
	checked := 0
	for _, def := range ir.AllIntrinsics() {
		if def.Pkg == drawPkg || def.Name == errorRaiseID {
			continue
		}
		fn := intrinsicDecl(def.Name)
		if fn == nil {
			t.Errorf("%s is registered but no declaration answers to it", def.Name)
			continue
		}
		if fn.IntrinsicBodyUsable {
			continue
		}
		checked++
		implemented := false
		for _, lang := range langs {
			if codegen.LookupIntrinsic(lang, def.Name) != nil {
				implemented = true
				break
			}
		}
		if !implemented {
			t.Errorf("#[intrinsic(%q)] is implemented by no language and carries no `usable` body; "+
				"a build reaching it emits a call to a function that does not exist", def.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no intrinsic was held to the emitter contract; the walk found nothing")
	}
}

const (
	drawPkg      = "sngl:internal/draw"
	errorRaiseID = "error.raise"
)

// intrinsicDecl finds the declaration carrying an intrinsic id, for the facts
// the registry does not record.
func intrinsicDecl(id string) *ir.Func {
	for _, name := range append(lib.Packages(), loadedTargetPackages()...) {
		pkg := checker.LibPackage(name)
		if pkg == nil {
			continue
		}
		for _, fn := range pkg.Funcs {
			if fn.Intrinsic == id {
				return fn
			}
		}
		for _, sd := range pkg.Structs {
			for _, m := range sd.Methods {
				if m.Intrinsic == id {
					return m
				}
			}
		}
	}
	return nil
}

func loadedTargetPackages() []string {
	var out []string
	for _, p := range codegen.CollectPlatforms() {
		out = append(out, "platform/"+p.PlatformIdentifier())
	}
	for _, name := range codegen.Langs() {
		out = append(out, "language/"+name)
	}
	return out
}
