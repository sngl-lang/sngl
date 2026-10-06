package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A tree is a declaration and nothing here knows what drawing is, so a tree
// with nothing to do with it works the same way. This stub declares the
// rich-text tree the mechanism was generalised for: a `document` hosts `block`
// members, a `para` is a block that hosts `inline` members, and a `bold` is an
// inline.
//
// Membership is the return position; what a component hosts is its default
// slot's type, which is how a member hosts a different family from its own.
const treeStubSource = `
import build "sngl:build"
import ui "sngl:ui"

component block build.family

component inline build.family

component document(children ...component block) ui.node {}

component para(children ...component inline) block {}

component bold(weight int) inline {}

component plain() ui.node {}
`

func treeStubConfig(t *testing.T) *checker.Config {
	t.Helper()
	doc, err := parser.Parse("richtext.sngl", []byte(treeStubSource))
	if err != nil {
		t.Fatalf("parse stub: %v", err)
	}
	return &checker.Config{
		IsMain:     true,
		LibSources: map[string][]*ast.Document{"richtext": {doc}},
	}
}

func checkTreeStub(t *testing.T, body string) []string {
	t.Helper()
	src := "import . \"sngl:ui\"\nimport . \"sngl:richtext\"\n" + body
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, treeStubConfig(t))
	var errs []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			errs = append(errs, d.Msg)
		}
	}
	return errs
}

func TestTreeChildrenAcceptsItsOwnKind(t *testing.T) {
	errs := checkTreeStub(t, `
component main node {
    document() {
        para() {
            bold(weight=700) {}
        }
    }
}
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestTreeChildrenRejectsAForeignKind(t *testing.T) {
	// bold is an inline, not a block: right tree, wrong segment.
	errs := checkTreeStub(t, `
component main node {
    document() {
        bold(weight=700) {}
    }
}
`)
	want := "expected block component in document, got bold"
	if !hasErr(errs, want) {
		t.Errorf("want %q, got %v", want, errs)
	}
}

func TestTreeChildrenRejectsAnUnmarkedComponent(t *testing.T) {
	errs := checkTreeStub(t, `
component main node {
    para() {
        plain() {}
    }
}
`)
	want := "expected inline component in para, got plain"
	if !hasErr(errs, want) {
		t.Errorf("want %q, got %v", want, errs)
	}
}

// A member hosts nothing unless it says so. The implicit "a shape contains
// shapes" rule is gone: the default slot is where hosting is declared, and
// `bold` declares none.
func TestAMemberWithNoSlotHostsNothing(t *testing.T) {
	errs := checkTreeStub(t, `
component main node {
    bold(weight=1) {
        bold(weight=2)
    }
}
`)
	want := "does not accept children"
	if !hasErr(errs, want) {
		t.Errorf("want %q, got %v", want, errs)
	}
}

func hasErr(errs []string, want string) bool {
	for _, e := range errs {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}
