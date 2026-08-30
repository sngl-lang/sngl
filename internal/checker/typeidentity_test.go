package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkedType resolves a named type out of a freshly checked package. Each call
// is its own Check, which is the point: every check loads its own copy of the
// embedded library, so two calls give two declarations of one library type.
func checkedType(t *testing.T, src, name string) *ir.Type {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %v", d.Error())
		}
	}
	sym, ok := pkg.Symbols.LookupType(name)
	if !ok {
		t.Fatalf("no type %q in the checked package", name)
	}
	return sym.SymType()
}

// One library declaration is one type however many times the library was
// loaded. It used to be as many types as there were checkers in the process,
// which reached users as `cannot initialize duration with duration` once the
// Go importer started handing out a type registered by a different check.
func TestLibraryTypeSurvivesASecondLoad(t *testing.T) {
	const src = `component main { text(value="x") }`
	for _, name := range []string{"duration", "datetime", "Style"} {
		a := checkedType(t, src, name)
		b := checkedType(t, src, name)
		if a.Decl == b.Decl {
			t.Fatalf("%s: two checks shared one declaration, so this asserts nothing", name)
		}
		if !a.Equal(b) {
			t.Errorf("%s from two checks is not one type", name)
		}
		if !a.IsAssignableTo(b) {
			t.Errorf("%s from one check is not assignable to %s from another", name, name)
		}
	}
}

// Unit arithmetic asks the same identity question Equal does, so it has to get
// the same answer: `pure.Wait() + 5ms` mixes a unit type registered by whatever
// check ran first with one the current check declared.
func TestLibraryUnitArithmeticSurvivesASecondLoad(t *testing.T) {
	const src = `component main { text(value="x") }`
	a := checkedType(t, src, "duration")
	b := checkedType(t, src, "duration")
	if !a.SameUnitType(b) {
		t.Error("duration from two checks is not the same unit")
	}
	mine := checkedType(t, `unit duration { tick }
component main { text(value="x") }`, "duration")
	if mine.SameUnitType(a) {
		t.Error("a program's own unit duration is the same unit as the library's")
	}
}

// A program's own declaration is not the library declaration it shadows, even
// though the two print the same name. This is the case an identity of
// (package, name) could be made too broad and quietly unify.
func TestShadowingDeclarationIsADifferentType(t *testing.T) {
	const shadow = `unit duration { tick }
component main { text(value="x") }`
	mine := checkedType(t, shadow, "duration")
	theirs := checkedType(t, `component main { text(value="x") }`, "duration")
	if mine.Equal(theirs) {
		t.Error("a program's own unit duration is equal to the library's")
	}
	if mine.IsAssignableTo(theirs) || theirs.IsAssignableTo(mine) {
		t.Error("a program's own unit duration is assignable across the library's")
	}
	// Two checks of the *same* program are still two programs: a declaration
	// with no package identity has nothing but its pointer to go on.
	if mine.Equal(checkedType(t, shadow, "duration")) {
		t.Error("two programs' unit duration declarations are equal")
	}
}

// The diagnostic for that mismatch has to name both declarations. Naming
// neither -- "cannot initialize duration with duration" -- is what hid the
// identity bug this test's siblings cover.
func TestShadowedTypeDiagnosticNamesBoth(t *testing.T) {
	doc, err := parser.Parse("test.sngl", []byte(withStd(`unit duration { tick }
var shadowed duration = 3ms
component main { text(value="x") }`)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	var msgs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			msgs = append(msgs, d.Msg)
		}
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "cannot initialize duration (test.sngl:") {
		t.Errorf("diagnostic does not say where the program's duration was declared:\n%s", joined)
	}
	if !strings.Contains(joined, "with duration (sngl:time)") {
		t.Errorf("diagnostic does not say the other duration is the library's:\n%s", joined)
	}
}

// `time` is declared by sngl:time and is not ambient, so naming it without
// that import is an error rather than a silent `dyn`. The resolver used to
// return dyn for the string-representable types whenever they were not in
// scope -- a fallback meant for stdlib bootstrap that also swallowed a missing
// import in user source, typing the declaration as dyn and building anyway.
func TestUnimportedTimeIsAnErrorNotDyn(t *testing.T) {
	doc, err := parser.Parse("test.sngl", []byte("component main {\n    var x time\n}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	var msg string
	for _, d := range diags {
		if d.Severity == ir.Error {
			msg = d.Msg
			break
		}
	}
	if msg == "" {
		t.Fatal("naming an unimported `time` checked clean; it must not resolve to dyn")
	}
	// The hint names the package that declares the type. sngl:i18n binds a
	// `time` formatter and sorts first, so a hint that ignores the role the
	// name was read in points at the wrong import.
	if !strings.Contains(msg, `sngl:time`) {
		t.Errorf("hint = %q, want it to name sngl:time", msg)
	}
	if strings.Contains(msg, "sngl:i18n") {
		t.Errorf("hint = %q, names i18n's `time` formatter rather than the type", msg)
	}
}

// The i18n formatters take the types they format, not `dyn`.
//
// Moving `time` out of the ambient tier made this fail silently: lib/i18n
// declares a func named `time`, so the bare type name resolved to that
// declaration, and the string-representable fallback typed the parameter
// `dyn` rather than reporting the missing import. The package qualifies the
// three types now; this asserts the result rather than the import, so it
// holds however they are reached.
func TestI18nFormattersTakeTheirOwnTypes(t *testing.T) {
	want := map[string]string{"date": "date", "time": "time", "datetime": "datetime"}
	pkg := checker.LibPackage("i18n")
	if pkg == nil {
		t.Fatal("sngl:i18n did not load")
	}
	seen := map[string]bool{}
	for _, fn := range pkg.Funcs {
		w, ok := want[fn.Name]
		if !ok || len(fn.Params) == 0 {
			continue
		}
		seen[fn.Name] = true
		if got := fn.Params[0].Type.String(); got != w {
			t.Errorf("i18n.%s takes %s, want %s", fn.Name, got, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("no i18n.%s formatter found", name)
		}
	}
}
