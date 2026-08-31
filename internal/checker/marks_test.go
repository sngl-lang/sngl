package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
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
	wantMarkErr(t, errs, "macro builtin: expected 1 argument, got 0")
}

func TestIntrinsicMarkStampsIdAndFlags(t *testing.T) {
	pkg, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout", usable, mutatesReceiver)]
func shout(s string) => s
`)
	wantNoMarkErrs(t, errs)
	fn := pkg.Funcs[0]
	if fn.Intrinsic != "markstub.shout" || !fn.IntrinsicBodyUsable || !fn.MutatesReceiver {
		t.Errorf("intrinsic = %+v, want the id and both flags", fn)
	}
}

func TestIntrinsicMarkRejectsAnUnknownFlag(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout", nosuch)]
func shout(s string) => s
`)
	wantMarkErr(t, errs, `unknown value "nosuch" (want one of: usable, mutates, readonly, mutatesReceiver)`)
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
#[intrinsic("markstub.shout", usable, usable)]
func shout(s string) => s
`)
	wantMarkErr(t, errs, "repeats flag usable")
}

// An id names a native implementation of a call, and a struct has no call.
func TestIntrinsicMarkCannotMarkAStruct(t *testing.T) {
	_, errs := checkMarkStub(t, markImports+`
#[intrinsic("markstub.shout")]
struct Tiny {}
`)
	wantMarkErr(t, errs, `#[intrinsic("markstub.shout")] cannot mark`)
}

// The alias is the file's, and the mark follows it like any other qualified
// name.
func TestTreeMarksFollowTheImportAlias(t *testing.T) {
	pkg, errs := checkMarkStub(t, `import t "sngl:tree"

#[t.kind]
struct block {}

component para() block {}
`)
	wantNoMarkErrs(t, errs)
	var tree *ir.StructDef
	for _, sd := range pkg.Structs {
		if sd.Name == "block" {
			tree = sd
		}
	}
	if tree == nil || !tree.IsTree {
		t.Fatalf("block was not marked as a tree")
	}
	if pkg.Components[0].Tree != tree {
		t.Errorf("para.Tree = %v, want the block declaration", pkg.Components[0].Tree)
	}
}

// A tree is named by a struct: the declaration is the identity, so there is
// nothing for a component to carry the mark for.
func TestTreeKindCannotMarkAComponent(t *testing.T) {
	_, errs := checkMarkStub(t, `import t "sngl:tree"

#[t.kind]
component para() {}
`)
	wantMarkErr(t, errs, "a tree is named by a struct")
}

// The struct is the tree and holds nothing; fields would suggest a value.
func TestTreeKindRefusesFields(t *testing.T) {
	_, errs := checkMarkStub(t, `import t "sngl:tree"

#[t.kind]
struct block {
    n int
}
`)
	wantMarkErr(t, errs, "a tree struct holds nothing")
}

// A misspelled tree is an unresolved name where it is written, which is the
// whole reason a tree is a declaration rather than a string.
func TestAMisspelledTreeIsUnresolved(t *testing.T) {
	_, errs := checkMarkStub(t, `import t "sngl:tree"

#[t.kind]
struct block {}

component para() blcok {}
`)
	wantMarkErr(t, errs, "blcok")
}

// #[foreign] is declared in sngl:macro, so it is the one compiler mark a
// program can write for itself.
func checkForeign(t *testing.T, body string) (*ir.Package, []string) {
	t.Helper()
	doc, err := parser.Parse("main.sngl", []byte("import std \"sngl:macro\"\n"+body))
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
    #[std.foreign("Title")]
    heading string = ""
}
`)
	wantNoMarkErrs(t, errs)
	sd := pkg.Structs[0]
	if sd.Foreign.Scheme != "js" || sd.Foreign.Path != "example.com/api" || sd.Foreign.Name != "Entry" || !sd.Foreign.Marked {
		t.Errorf("struct foreign = %+v", sd.Foreign)
	}
	// One argument is the name alone: a field has no package of its own.
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
#[std.foreign("js:example.com/api", "add", pure, async)]
func add(a int, b int) => a + b
`)
	wantNoMarkErrs(t, errs)
	fn := pkg.Funcs[0]
	if !fn.IsAsync || fn.Purity != ir.PurityPure || fn.Foreign.Name != "add" {
		t.Errorf("func = async %v purity %v foreign %+v", fn.IsAsync, fn.Purity, fn.Foreign)
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

component App {
    #[std.foreign("js:example.com/api", "Double", pure)]
    func Row.double(x int) => x * 2
}
`)
	wantNoMarkErrs(t, errs)
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.Receiver == "Row" {
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
    #[std.foreign("Title")]
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
	wantMarkErr(t, errs, "macro std.foreign: expected at least 1 argument, got 0")
}

func TestForeignRejectsAnUnknownFlag(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign("js:x", "add", nosuch)]
func add(a int) => a
`)
	wantMarkErr(t, errs, `unknown value "nosuch" (want one of: pure, async)`)
}

// The flags describe a call, and a struct has none.
func TestForeignRefusesFlagsOnANonFunc(t *testing.T) {
	_, errs := checkForeign(t, `
#[std.foreign("js:x", "Entry", pure)]
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
