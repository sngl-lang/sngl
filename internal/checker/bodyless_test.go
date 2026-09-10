package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A function body is optional, and the checker requires the answer to come from
// somewhere rather than assuming it does.
//
// The rule replaces the shape it is easy to reach for instead: suppressing the
// missing-return diagnostic whenever an #[intrinsic] mark is present. That reads
// as "mark present, skip a rule", and it leaves the declaration claiming a body
// nobody reads — the fabricated `{ return 0 }` that looks exactly like a real
// implementation and becomes one the moment somebody adds `usable`.

// A signature with nothing to supply its body is reported.
func TestBodylessFuncNeedsASource(t *testing.T) {
	errs := checkSrc(t, "func f() int\n")
	if len(errs) == 0 {
		t.Fatal("a bodyless func with no intrinsic, foreign mark or override checked clean")
	}
	if got := errs[0].Error(); !strings.Contains(got, "no body") {
		t.Errorf("unexpected diagnostic: %s", got)
	}
}

// Void is not a loophole: the absence of a return type says nothing about where
// the body comes from.
func TestBodylessVoidFuncNeedsASource(t *testing.T) {
	if errs := checkSrc(t, "func f()\n"); len(errs) == 0 {
		t.Fatal("a bodyless void func checked clean")
	}
}

// A program cannot write #[intrinsic] at all, because it cannot import the
// package that declares it. That used to be only half true: the mark resolver
// read the import statement rather than the import, so the mark applied while
// the import beside it was rejected. Resolving marks through the scope closed
// that — a name a program may not import is not in its scope.
//
// The bodyless-intrinsic case the library relies on is exercised by the library
// itself, which is all signatures now; #[foreign(..., native)] is the form a
// program can write, in TestForeignNativeNeedsNoBody.
func TestCompilerTierMarkIsNotAvailableToAProgram(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length")]
func length(s string) int
`
	errs := checkSrc(t, src)
	if len(errs) == 0 {
		t.Fatal("a program used a compiler-tier mark")
	}
	var sawImport bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "internal to the compiler") {
			sawImport = true
		}
	}
	if !sawImport {
		t.Errorf("the import was not the complaint: %s", errs[0].Error())
	}
}

// A written body is itself the assertion that it computes the same answer the
// native implementation would, so a backend without the id may emit it. There is
// no flag beside it: `usable` was a second record of one fact, and it sat one
// word away from promoting a fabricated `return 0` into a live wrong answer.
// Making the body optional is what let the flag go.
func TestIntrinsicMayCarryItsOwnImplementation(t *testing.T) {
	src := `import . "sngl:internal/marks"

#[intrinsic("string.length")]
func length(s string) int {
    return 0
}
`
	for _, e := range checkSrc(t, src) {
		if strings.Contains(e.Error(), "body") {
			t.Fatalf("a body on an intrinsic was reported: %s", e.Error())
		}
	}
}

// A component's body is optional too, and `{}` is not the same declaration as
// no braces: the first renders nothing, the second says the render comes from
// elsewhere.
func TestBodylessComponentNeedsASource(t *testing.T) {
	errs := checkSrc(t, "component gap(w int) node\n")
	if len(errs) == 0 {
		t.Fatal("a bodyless component with no override checked clean")
	}
	if got := errs[0].Error(); !strings.Contains(got, "no body") {
		t.Errorf("unexpected diagnostic: %s", got)
	}
}

// The empty body is the other half of the pair and stays legal: a component
// that renders nothing is a thing a declaration may say.
func TestEmptyBodiedComponentIsNotBodyless(t *testing.T) {
	if errs := checkSrc(t, "component gap(w int) node {}\n"); len(errs) != 0 {
		t.Fatalf("an empty-bodied component was reported: %v", errs)
	}
}

// ir.Component.Bodyless is what carries the distinction past the checker.
// ast.StmtBlock.IsDefined() cannot: it reports whether a block came from
// source, so everything ir.Convert rebuilt looked bodyless and every
// empty-bodied component printed back as a signature -- which then failed to
// re-check, because the rule above is exactly what it tripped.
//
// The flag is stamped at registration, so it is read here from a package that
// also carries the diagnostic; what is under test is the fact, not the rule.
func TestBodylessSurvivesIntoIR(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"no braces", "component gap(w int) node\n", true},
		{"empty body", "component gap(w int) node {}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.Parse("test.sngl", []byte(withStd(tc.src)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
			var comp *ir.Component
			for _, c := range pkg.Components {
				if c.Name == "gap" {
					comp = c
				}
			}
			if comp == nil {
				t.Fatal("component gap was not registered")
			}
			if comp.Bodyless != tc.want {
				t.Errorf("Bodyless = %v, want %v", comp.Bodyless, tc.want)
			}
		})
	}
}

// A library declaration is asked the same question, and asked it per target:
// the build's targets are resolved by then, so an override for one of them is
// not an answer for the others. This is what makes a missing platform
// implementation a build failure rather than a shape that renders nothing.
//
// Exercised through a stub platform package, because nothing in lib/ is
// bodyless yet -- the conversion in #213 is what makes it load-bearing.
func TestBodylessLibComponentNeedsAnImplementationPerTarget(t *testing.T) {
	const extSource = `
import sngl "sngl:ui"

component blip(x int) sngl.node
`
	const userSource = `
component main node {
    text(value="hi")
}
`
	doc, err := parser.Parse("main.sngl", []byte(withStd(userSource)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg := extStubConfig(t, extSource)
	cfg.Targets = []ir.StaticTarget{{Platform: "extstub"}}
	_, diags := checker.Check(doc, cfg)
	var found string
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "no implementation for") {
			found = d.Msg
		}
	}
	if found == "" {
		t.Fatalf("a bodyless lib component with no override for the target checked clean; diags: %v", diags)
	}
	if !strings.Contains(found, "extstub") {
		t.Errorf("diagnostic does not name the target that lacks one: %s", found)
	}
}

// And it is satisfied by an override from the platform's own package, which is
// the shape the stdlib shapes take after the conversion: the declaration lives
// in one library package and each target implements it in its own.
//
// Written with a prop selection on purpose: a platform package's override may
// carry one, and testing HasParens alone used to drop it here silently.
func TestBodylessLibComponentSatisfiedByAPlatformOverride(t *testing.T) {
	const libSource = `
import sngl "sngl:ui"

component blip(x int) sngl.node
`
	const extSource = `
import sngl "sngl:ui"
import bk "sngl:blipkit"

component bk.blip[extstub.platform](x) {
    sngl.text(value="blip {x}")
}
`
	const userSource = `
component main node {
    text(value="hi")
}
`
	libDoc, err := parser.Parse("blipkit.sngl", []byte(withStd(libSource)))
	if err != nil {
		t.Fatalf("lib parse: %v", err)
	}
	extDoc, err := parser.Parse("extstub.sngl", []byte(withStd(extSource)))
	if err != nil {
		t.Fatalf("ext parse: %v", err)
	}
	doc, err := parser.Parse("main.sngl", []byte(withStd(userSource)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{extStubPlatform{}},
		Targets:   []ir.StaticTarget{{Platform: "extstub"}},
		LibSources: map[string][]*ast.Document{
			"blipkit":          {libDoc},
			"platform/extstub": {extDoc},
		},
	})
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "no implementation for") {
			t.Errorf("an override for the target being built did not satisfy the rule: %s", d.Msg)
		}
	}
}

// A bad selection on a platform package's override is now reported. Testing
// HasParens alone dropped the whole declaration before the selection was ever
// read, so a misspelled prop produced no override and no diagnostic -- the
// build succeeded with the base's body on every target.
func TestPlatformOverrideSelectionIsChecked(t *testing.T) {
	const libSource = `
import sngl "sngl:ui"

component blip(x int) sngl.node
`
	const extSource = `
import sngl "sngl:ui"
import bk "sngl:blipkit"

component bk.blip[extstub.platform](bogus) {
    sngl.text(value="blip")
}
`
	libDoc, err := parser.Parse("blipkit.sngl", []byte(withStd(libSource)))
	if err != nil {
		t.Fatalf("lib parse: %v", err)
	}
	extDoc, err := parser.Parse("extstub.sngl", []byte(withStd(extSource)))
	if err != nil {
		t.Fatalf("ext parse: %v", err)
	}
	doc, err := parser.Parse("main.sngl", []byte(withStd("component main node {\n    text(value=\"hi\")\n}\n")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{extStubPlatform{}},
		Targets:   []ir.StaticTarget{{Platform: "extstub"}},
		LibSources: map[string][]*ast.Document{
			"blipkit":          {libDoc},
			"platform/extstub": {extDoc},
		},
	})
	var found bool
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, "bogus") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a platform override selecting a prop the base does not declare was not reported; diags: %v", diags)
	}
}

// The converse of the bodyless rule: #[intrinsic] answers where a component's
// render comes from, so a body beside one is emitted by nobody and read by
// nobody. `{}` is refused with the rest, because it says the component renders
// nothing -- the one thing an intrinsic never does.
//
// A program cannot write the mark, so this is exercised through a platform
// package, which is also where every intrinsic component actually lives.
func TestIntrinsicComponentMayNotHaveABody(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"empty body", "{}", true},
		{"real body", "{ sngl.text(value=\"x\") }", true},
		{"no body", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := "\nimport sngl \"sngl:ui\"\nimport marks \"sngl:internal/marks\"\n\n" +
				"#[marks.intrinsic(\"extstub:Thing\")]\ncomponent Thing(x int) sngl.node " + tc.body + "\n"
			doc, err := parser.Parse("main.sngl", []byte(withStd("component main node {\n    text(value=\"hi\")\n}\n")))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, diags := checker.Check(doc, extStubConfig(t, ext))
			var got string
			for _, d := range diags {
				if d.Severity == ir.Error && strings.Contains(d.Msg, "has a body") {
					got = d.Msg
				}
			}
			if tc.wantErr && got == "" {
				t.Fatalf("an #[intrinsic] component with a body was accepted; diags: %v", diags)
			}
			if !tc.wantErr && got != "" {
				t.Fatalf("a bodyless #[intrinsic] component was reported: %s", got)
			}
		})
	}
}
