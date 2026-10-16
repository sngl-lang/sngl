package checker_test

import (
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// The compiler's own marks are declared under sngl:internal/, which only
// library source may import. A stub package substituted through
// Config.LibSources is the only place a fixture can write one, so these cases
// are checked as a library package rather than as a program.
func checkMarkStub(t *testing.T, src string) (*ir.Package, []string) {
	t.Helper()
	stub, err := parser.Parse("markstub.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse stub: %v", err)
	}
	main, err := parser.Parse("main.sngl", []byte("import ms \"sngl:markstub\"\n"))
	if err != nil {
		t.Fatalf("parse main: %v", err)
	}
	pkg, diags := checker.Check(main, &checker.Config{
		LibSources: map[string][]*ast.Document{"markstub": {stub}},
	})
	var errs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d.Msg)
		}
	}
	var stubPkg *ir.Package
	if pkg != nil && len(pkg.Imports) > 0 {
		stubPkg = pkg.Imports[0].Pkg
	}
	return stubPkg, errs
}

func wantMarkErr(t *testing.T, errs []string, want string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e, want) {
			return
		}
	}
	t.Errorf("want an error containing %q, got %v", want, errs)
}

func wantNoMarkErrs(t *testing.T, errs []string) {
	t.Helper()
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

const markImports = "import . \"sngl:internal/marks\"\n"

// The kind is stamped on whatever IR the declaration became, and the marked
// declaration is what the compiler then keys on.
func TestBuiltinMarkStampsTheKind(t *testing.T) {
	pkg, errs := checkMarkStub(t, markImports+`
#[builtin("int")]
struct Tiny {}
`)
	wantNoMarkErrs(t, errs)
	if len(pkg.Structs) != 1 || pkg.Structs[0].Builtin != ir.BuiltinInt {
		t.Fatalf("Builtin = %q, want int", pkg.Structs[0].Builtin)
	}
}

func TestBuiltinMarkRejectsAnUnknownKind(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[builtin("nosuch")]
struct Tiny {}
`)
	wantMarkErr(t, errs, `unknown builtin kind "nosuch"`)
}

// A kind names an IR construct, and an enum dispatches to none.
func TestBuiltinMarkRejectsAnUnsupportedForm(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[builtin("int")]
enum Tiny { a }
`)
	wantMarkErr(t, errs, `#[builtin("int")] cannot mark`)
}

// The kind is declared `kind string`, so a bare identifier is not one.
func TestBuiltinMarkRejectsABareIdent(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[builtin(int)]
struct Tiny {}
`)
	wantMarkErr(t, errs, `macro builtin: argument "kind"`)
}

func TestBuiltinMarkRejectsTheWrongArity(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[builtin]
struct Tiny {}
`)
	wantMarkErr(t, errs, "macro builtin: missing required argument \"kind\"")
}

func TestIntrinsicMarkStampsIdAndFlags(t *testing.T) {
	pkg, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout", mutatesReceiver)]
func shout(s string) => s
`)
	wantNoMarkErrs(t, errs)
	fn := pkg.Funcs[0]
	if fn.Intrinsic != "markstub.shout" || !fn.MutatesReceiver {
		t.Errorf("intrinsic = %+v, want the id and the flag", fn)
	}
}

func TestIntrinsicMarkRejectsAnUnknownFlag(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout", nosuch)]
func shout(s string) => s
`)
	wantMarkErr(t, errs, `unknown value "nosuch" (want one of: mutates, readonly, mutatesReceiver, build)`)
}

func TestIntrinsicMarkRejectsContradictoryFlags(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout", mutates, readonly)]
func shout(s string) => s
`)
	wantMarkErr(t, errs, "is both mutates and readonly")
}

func TestIntrinsicMarkRejectsARepeatedFlag(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout", mutates, mutates)]
func shout(s string) => s
`)
	wantMarkErr(t, errs, "repeats flag mutates")
}

// On a struct the id names a host type one target spells: no call, so a flag
// describing one has nothing to describe.
func TestIntrinsicMarkOnAStruct(t *testing.T) {
	pkg, errs := checkMarkStub(t, markImports+`
#[intrinsic("stub:tiny")]
struct Tiny {}
`)
	wantNoMarkErrs(t, errs)
	i := slices.IndexFunc(pkg.Structs, func(sd *ir.StructDef) bool { return sd.Name == "Tiny" })
	if i < 0 {
		t.Fatal("Tiny not declared")
	}
	if got := pkg.Structs[i].Intrinsic; got != "stub:tiny" {
		t.Errorf("Tiny.Intrinsic = %q, want %q", got, "stub:tiny")
	}
	_, errs = checkMarkStub(t, markImports+`
#[intrinsic("stub:tiny", mutates)]
struct Tiny {}
`)
	wantMarkErr(t, errs, "which describes a call; a struct has none")
}

// A family is named through the file's alias like any other qualified name,
// and a member's Tree is the family's declaration.
func TestFamilyFollowsTheImportAlias(t *testing.T) {
	pkg, errs := checkMarkStub(t, `import b "sngl:build"

component para() block {}

component block b.family
`)
	wantNoMarkErrs(t, errs)
	var family, para *ir.Component
	for _, c := range pkg.Components {
		switch c.Name {
		case "block":
			family = c
		case "para":
			para = c
		}
	}
	if !family.IsFamily() {
		t.Fatalf("block is not a family")
	}
	if para.Family != family {
		t.Errorf("para.Tree = %v, want the block declaration", para.Family)
	}
}

// A misspelled tree is an unresolved name where it is written, which is the
// whole reason a tree is a declaration rather than a string.
func TestAMisspelledTreeIsUnresolved(t *testing.T) {
	_, errs := checkMarkStub(t, `import t "sngl:family"

#[t.crosses]
struct block {}

component para() blcok {}
`)
	wantMarkErr(t, errs, "blcok")
}

// #[foreign] is declared in sngl:macro, so it is the one compiler mark a
// program can write for itself.
func checkForeign(t *testing.T, body string) (*ir.Package, []string) {
	t.Helper()
	doc, err := parser.Parse("main.sngl", []byte("import std \"sngl:macro\"\nimport ui \"sngl:ui\"\n"+body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	var errs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d.Msg)
		}
	}
	return pkg, errs
}

func TestForeignMarksStructAndField(t *testing.T) {
	pkg, errs := checkForeign(t, `
#[std.foreign("js:example.com/api", "Entry")]
struct Post {
    #[std.foreign(name="Title")]
    heading string = ""
}
`)
	wantNoMarkErrs(t, errs)
	sd := pkg.Structs[0]
	if sd.Foreign.Scheme != "js" || sd.Foreign.Path != "example.com/api" || sd.Foreign.Name != "Entry" || !sd.Foreign.Marked {
		t.Errorf("struct foreign = %+v", sd.Foreign)
	}
	// A field names its name alone: it has no package of its own.
	if f := sd.Fields[0]; f.Foreign.Name != "Title" || f.Foreign.Path != "" {
		t.Errorf("field foreign = %+v", f.Foreign)
	}
	// A mark never confers type identity, so it sets no origin.
	if sd.Foreign.Origin != nil {
		t.Error("a mark set an Origin; only a scheme importer unifies declarations")
	}
}

func TestForeignMarksFuncFlags(t *testing.T) {
	pkg, errs := checkForeign(t, `
#[std.foreign("js:example.com/api", "add", async)]
const func add(a int, b int) => a + b
`)
	wantNoMarkErrs(t, errs)
	fn := pkg.Funcs[0]
	if !fn.IsAsync || fn.Purity != ir.PurityPure || !fn.Const || fn.Foreign.Name != "add" {
		t.Errorf("func = async %v purity %v foreign %+v", fn.IsAsync, fn.Purity, fn.Foreign)
	}
}

// A mark's arguments bind as a call's do: by position, by name in any order,
// and the trailing flags as a list by name.
func TestForeignMarkArgsByName(t *testing.T) {
	pkg, errs := checkForeign(t, `
#[std.foreign(name="Entry", path="js:example.com/api")]
struct Post {}

#[std.foreign("js:example.com/api", name="add", flags=[async])]
const func add(a int, b int) => a + b
`)
	wantNoMarkErrs(t, errs)
	if f := pkg.Structs[0].Foreign; f.Scheme != "js" || f.Path != "example.com/api" || f.Name != "Entry" {
		t.Errorf("struct foreign = %+v", f)
	}
	if fn := pkg.Funcs[0]; !fn.IsAsync || fn.Foreign.Name != "add" {
		t.Errorf("func = async %v foreign %+v", fn.IsAsync, fn.Foreign)
	}
}

// A nested func with an explicit receiver stays a method on the named type
// rather than being desugared onto the surrounding one, and a mark written on
// it means what it would at top level.
func TestForeignMarksNestedMethodWithExplicitReceiver(t *testing.T) {
	pkg, errs := checkForeign(t, `
struct Row {
    n int = 0
}

component App ui.node {
    #[std.foreign("js:example.com/api", "Double")]
    const func Row.double(x int) => x * 2
}
`)
	wantNoMarkErrs(t, errs)
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.RecvName() == "Row" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatalf("no method on Row in %d funcs", len(pkg.Funcs))
	}
	if !fn.Foreign.Marked || fn.Foreign.Name != "Double" || fn.Purity != ir.PurityPure {
		t.Errorf("nested method foreign = %+v purity %v", fn.Foreign, fn.Purity)
	}
}

// One foreign name cannot stand for several declarations.
func TestForeignRefusesSeveralNames(t *testing.T) {
	_, errs := checkForeign(t, `
struct Post {
    #[std.foreign(name="Title")]
    a, b string = ""
}
`)
	wantMarkErr(t, errs, "marks 2 names at once")
}

func TestForeignRequiresAName(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign]
struct Post {}
`)
	wantMarkErr(t, errs, "#[foreign] requires a non-empty name")
}

func TestForeignRejectsAnUnknownFlag(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign("js:x", "add", nosuch)]
func add(a int) => a
`)
	wantMarkErr(t, errs, `unknown value "nosuch" (want one of: async, native)`)
}

// The flags describe a call, and a struct has none.
func TestForeignRefusesFlagsOnANonFunc(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign("js:x", "Entry", async)]
struct Post {}
`)
	wantMarkErr(t, errs, "which describes a call")
}

// The mark says what a declaration is outside SNGL, and a declaration is one
// thing.
func TestForeignRefusesASecondMark(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign("js:x", "One")]
#[std.foreign("js:x", "Two")]
struct Post {}
`)
	wantMarkErr(t, errs, `already marked as "One"`)
}

// Struct, struct field and function are the forms the mark specifies; the
// others refuse it rather than carry it nowhere.
func TestForeignRefusesAnUnspecifiedForm(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign("js:x", "Px")]
unit length { px }
`)
	wantMarkErr(t, errs, `#[foreign("Px")] cannot mark`)
}

// A mark argument may name a constant, which is what lets a target package
// state its runtime's import path once instead of on every declaration that
// crosses into it -- `sngl:language/go` spells it twenty times otherwise.
//
// Read from the declarations rather than a scope: the marks on a struct shell
// are applied in pass1's first half and consts are not registered until its
// second, which is the same reason `ast.EvalString` never consults scope.
func TestForeignTakesAConstArgument(t *testing.T) {
	pkg, errs := checkForeign(t, `
const apiPkg = "go:example.com/api"

#[std.foreign(apiPkg, "api.Entry")]
struct Entry {
    title string = ""
}
`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, sd := range pkg.Structs {
		if sd.Name != "Entry" {
			continue
		}
		if sd.Foreign.Path != "example.com/api" {
			t.Fatalf("the const did not reach the mark: Foreign = %+v", sd.Foreign)
		}
		return
	}
	t.Fatal("no Entry struct registered")
}

// It composes with concatenation, because substituting the value leaves an
// ordinary constant string expression behind.
func TestForeignConcatenatesAConstArgument(t *testing.T) {
	pkg, errs := checkForeign(t, `
const base = "go:example.com"

#[std.foreign(base + "/api", "api.Entry")]
struct Entry {
    title string = ""
}
`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, sd := range pkg.Structs {
		if sd.Name == "Entry" && sd.Foreign.Path != "example.com/api" {
			t.Fatalf("the const did not compose: Foreign = %+v", sd.Foreign)
		}
	}
}

// A flag is an identifier whose spelling *is* the value, so a const of that
// name must not be substituted for it -- `async` stays the flag `async` even
// where something declares `const async`.
func TestForeignFlagIsNotAConstLookup(t *testing.T) {
	_, errs := checkForeign(t, `
const async = "not a flag"

#[std.foreign("js:x", "add", async)]
func add(a int) => a
`)
	if len(errs) > 0 {
		t.Fatalf("a flag was resolved as a const: %v", errs)
	}
}
