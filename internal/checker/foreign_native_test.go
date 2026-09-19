package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkedPkgWithTargets checks inline source with the targets registered and
// returns the package. A target's own library package — sngl:language/go, where
// #[go.native] is declared — exists only when that target is registered, so the
// default harness cannot see it.
func checkedPkgWithTargets(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	langs, plats := testtargets.Targets()
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	return pkg
}

// `native` is the difference between a declaration that *corresponds to* a
// foreign identifier and one that *is* it, and Marked is where the two already
// parted: a backend reads an unmarked foreign path as a reference to emit and
// import, and a marked one as a name to spell beside a declaration it emits
// itself.
//
// The consequence is where the import lands. One written at the top of a
// target's library package is resolved by every program built for that
// language — writing sngl:remote/http's Go transport as a plain `go:` import in
// golang.sngl made three html server-action tests fail on a package they have
// no interest in. A native declaration costs nothing until something calls it.
func TestGoNativeIsAReferenceNotADeclaration(t *testing.T) {
	src := `import . "sngl:macro"
import go "sngl:language/go"

#[go.native("strings", "ToUpper")]
func shout(s string) string

#[foreign("go:strings", "ToLower")]
func quiet(s string) string {
    return s
}
`
	pkg := checkedPkgWithTargets(t, src)

	native := findFn(t, pkg, "shout")
	if native.Foreign.Marked {
		t.Error("a native declaration is Marked; a backend would emit it instead of calling it")
	}
	if native.Foreign.Path != "strings" || native.Foreign.Name != "ToUpper" {
		t.Errorf("native foreign = %+v; want the bare package path and the identifier", native.Foreign)
	}
	if native.Foreign.Scheme != "go" {
		t.Errorf("scheme = %q; want the language the name belongs to", native.Foreign.Scheme)
	}

	// Without the flag nothing changes: the declaration is still the program's
	// own and the name is one to spell beside it.
	plain := findFn(t, pkg, "quiet")
	if !plain.Foreign.Marked {
		t.Error("an ordinary #[foreign] stopped being the program's own declaration")
	}
}

// A native declaration has no body and may not: the identifier already exists,
// so a body would be emitted by nobody and read by nobody. Bodylessness is
// legal because the mark is one of the answers to where a body comes from.
func TestGoNativeNeedsNoBody(t *testing.T) {
	src := `import go "sngl:language/go"

#[go.native("strings", "ToUpper")]
func shout(s string) string
`
	for _, e := range checkWithTargets(t, src) {
		if strings.Contains(e.Error(), "no body") || strings.Contains(e.Error(), "missing return") {
			t.Fatalf("a native declaration was told to grow a body: %s", e.Error())
		}
	}
}

// A declaration that *is* a foreign identifier has to say whose. An ordinary
// #[foreign] may leave the scheme off — it only records a correspondence — but a
// native one is emitted as a reference, and which language's reference is the
// question.
func TestGoNativeRequiresBothHalves(t *testing.T) {
	src := `import go "sngl:language/go"

#[go.native("", "ToUpper")]
func shout(s string) string
`
	errs := checkWithTargets(t, src)
	if len(errs) == 0 {
		t.Fatal("a native declaration naming no language checked")
	}
	// The one-argument form is the struct field's, so the diagnostic names
	// which half is missing and who may leave it out, rather than restating
	// that both are needed.
	if got := errs[0].Error(); !strings.Contains(got, "a package path is required") {
		t.Errorf("unexpected diagnostic: %s", got)
	}
}

func findFn(t *testing.T, pkg *ir.Package, name string) *ir.Func {
	t.Helper()
	for _, fn := range pkg.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	t.Fatalf("no func %q in the checked package", name)
	return nil
}
