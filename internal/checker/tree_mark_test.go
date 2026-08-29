package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The tree marks carry a name and nothing else, so a tree that has nothing to
// do with drawing works the same way. This stub declares the rich-text tree
// the mechanism was generalised for: a `document` hosts `block` nodes, a
// `para` is a block that hosts `inline` nodes, and a `bold` is an inline.
const treeStubSource = `
import tree "sngl:internal/tree"

#[tree.children("block")]
component document() {}

#[tree.kind("block")]
#[tree.children("inline")]
component para() {}

#[tree.kind("inline")]
component bold(weight int) {}

component plain() {}
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
	src := "import . \"sngl:std\"\nimport . \"sngl:richtext\"\n" + body
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
component main {
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
component main {
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
component main {
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

// A member with no children mark of its own hosts its own kind, so a nested
// node needs no second mark to be legal — and a member that carries #[children]
// hosts that kind instead, which is what makes para a block full of inlines.
func TestTreeKindImpliesItsOwnChildren(t *testing.T) {
	errs := checkTreeStub(t, `
component main {
    document() {
        para() {}
        para() {
            para() {}
        }
    }
}
`)
	want := "expected inline component in para, got para"
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
