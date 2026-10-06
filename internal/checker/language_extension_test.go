package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// extStubLang is a language that ships a package, the way extStubPlatform is a
// platform that ships one. There is no lib/language/extlang directory, so the
// source reaches the checker through Config.LibSources.
type extStubLang struct{ name string }

func (l extStubLang) LanguageIdentifier() string { return l.name }
func (extStubLang) Description() string          { return "language extension test stub" }

// The transport sngl:remote/http declares and leaves open, implemented by a
// language. The body reads the package's own declaration rather than one of
// its imports, as a target package's override does: an override body resolves
// in the package, and a package's imports are its files' own.
const langExtSource = `
import http "sngl:remote/http"

func stub() http.Result {
    return http.Result{status = 200, body = "stub", headers = {}}
}

func http.get[language]() {
    return stub()
}
`

const langExtProgram = `
import http "sngl:remote/http"

func body(url string) string {
    return http.get(url).body
}
`

// checkWithLanguages checks src with these languages registered and this
// target named, and answers the checked package.
func checkWithLanguages(t *testing.T, src string, target string, langs ...string) (*ir.Package, []ir.Diagnostic) {
	t.Helper()
	ext, err := parser.Parse("extlang.sngl", []byte(langExtSource))
	if err != nil {
		t.Fatalf("stub language package parse: %v", err)
	}
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg := &checker.Config{
		IsMain:     true,
		LibSources: map[string][]*ast.Document{},
		Targets:    []ir.StaticTarget{{Language: target}},
	}
	for _, name := range langs {
		cfg.Languages = append(cfg.Languages, extStubLang{name: name})
		cfg.LibSources["language/"+name] = []*ast.Document{ext, targetNodeDoc(t, "language/"+name)}
	}
	return checker.Check(doc, cfg)
}

// libFunc is the function a library package declares, reached through the
// namespace the program imported it under.
func libFunc(t *testing.T, pkg *ir.Package, ns, name string) *ir.Func {
	t.Helper()
	sym, found := pkg.Symbols.Root.Lookup(ns)
	if !found {
		t.Fatalf("no namespace %q in the checked package", ns)
	}
	namespace, isNS := sym.(*ir.Namespace)
	if !isNS || namespace.Pkg == nil {
		t.Fatalf("%q is not an imported package", ns)
	}
	member, found := namespace.Pkg.Symbols.LookupMember(name)
	if !found {
		t.Fatalf("no %s.%s", ns, name)
	}
	fn, isFunc := member.(*ir.Func)
	if !isFunc {
		t.Fatalf("%s.%s is not a function", ns, name)
	}
	return fn
}

// A language's package is loaded the way a platform's is -- building for a
// target is an `import _ "sngl:language/<it>"` nobody wrote -- so an override
// in it is merged into the declaration it names. That is what makes a language
// able to implement a declaration the library leaves open, rather than only
// describe types: sngl:remote/http declares `get` and no tier below a target
// can say what it does.
func TestLanguagePackageOverrideMerges(t *testing.T) {
	pkg, diags := checkWithLanguages(t, langExtProgram, "extlang", "extlang")
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected error: %s", d.Error())
		}
	}
	get := libFunc(t, pkg, "http", "get")
	body, ok := get.LanguageOverrides["extlang"]
	if !ok {
		t.Fatalf("no implementation of http.get for extlang; have %v", keys(get.LanguageOverrides))
	}
	if len(body.Stmts) == 0 {
		t.Error("the override merged as an empty body: the body was reserved but never checked")
	}
	// The declaration itself stays open. ir.Specialize swaps the entry for the
	// target being built in, and until it does, a function with a body for one
	// target and none for another has to look like neither.
	if len(get.Block) != 0 {
		t.Errorf("http.get has a body before specialization: %d stmts", len(get.Block))
	}
}

// Only when targeted. A language's package is a side-effect import the build
// writes, so a build for another language never writes it -- and an override in
// it would otherwise be merged by a build that cannot emit it.
func TestUntargetedLanguagePackageDoesNotMerge(t *testing.T) {
	pkg, diags := checkWithLanguages(t, langExtProgram, "other", "extlang", "other")
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("unexpected error: %s", d.Error())
		}
	}
	get := libFunc(t, pkg, "http", "get")
	if _, ok := get.LanguageOverrides["extlang"]; ok {
		t.Error("a build targeting `other` merged extlang's override")
	}
}
